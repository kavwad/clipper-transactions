// Package clipper lets you interact with your Clipper Card data.
//
// Example usage:
//	client := clipper.NewClient("email", "password")
//	// You can only access this page twice per day, per Clipper.
//	transactions := client.Transactions(context.TODO())
//	for card := range transactions {
//		fmt.Println("nickname:", card.Nickname)
//		fmt.Printf("txns: %#v\n", transactions[card].Transactions
//	}
//
// A PDF-to-CSV exporter lives at https://clipper-csv.appspot.com.
package clipper

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kevinburke/rest"
	pdfcontent "github.com/unidoc/unidoc/pdf/contentstream"
	"github.com/unidoc/unidoc/pdf/core"
	pdf "github.com/unidoc/unidoc/pdf/model"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/encoding/charmap"
)

// Found by trial and error from PDF.
var positions = []float64{
	28,
	133.71,
	359.24,
	479.05,
	528.38,
	655.88,
	685.78,
	722.22,
}

func findPositionIdx(pos float64) int {
	if pos < 0 || pos > 1100 {
		panic(fmt.Sprintf("invalid pos %v", pos))
	}
	if pos <= positions[0] {
		return 0
	}
	if pos >= positions[len(positions)-1] {
		return len(positions) - 1
	}
	for i := 0; i < len(positions)-1; i++ {
		halfway := positions[i] + (positions[i+1]-positions[i])/2
		if pos < halfway {
			return i
		}
	}
	return len(positions) - 1
}

func howManyTabs(prevPos, curPos float64) int {
	if prevPos >= curPos {
		panic("not expected")
	}
	idx := findPositionIdx(prevPos)
	idx2 := findPositionIdx(curPos)
	return idx2 - idx
}

func extractText(parser *pdfcontent.ContentStreamParser) (string, error) {
	operations, err := parser.Parse()
	if err != nil {
		return "", err
	}
	xPos, yPos := float64(-1), float64(-1)
	inText := false
	txt := ""
	// columnStarts:
	//  28.00 date
	// 133.71 transaction type
	// 359.24 location
	// 479.05 route
	// 528.38 product
	// 655.88 debit
	// 685.78 credit
	// 722.22 balance
	for _, op := range *operations {
		if op.Operand == "BT" {
			inText = true
		} else if op.Operand == "ET" {
			inText = false
		}
		if op.Operand == "Tm" {
			// Text matrix. See here for an explanation of how this relates to
			// drawn software:
			// https://stackoverflow.com/a/17202701/329700
			if len(op.Params) != 6 {
				continue
			}
			// 0-3 are scale/shear for x and y. Typical values are 1 0 0 1.
			// 4 is X offset from the left side.
			// 5 is Y offset from the bottom (origin in doc bottom left corner).
			xfloat, ok := op.Params[4].(*core.PdfObjectFloat)
			if !ok {
				xint, ok := op.Params[4].(*core.PdfObjectInteger)
				if !ok {
					continue
				}
				xfloat = core.MakeFloat(float64(*xint))
			}
			yfloat, ok := op.Params[5].(*core.PdfObjectFloat)
			if !ok {
				yint, ok := op.Params[5].(*core.PdfObjectInteger)
				if !ok {
					continue
				}
				yfloat = core.MakeFloat(float64(*yint))
			}
			if yPos == -1 {
				yPos = float64(*yfloat)
			} else if yPos > float64(*yfloat) {
				txt += "\n"
				xPos = float64(*xfloat)
				yPos = float64(*yfloat)
				continue
			}
			if xPos == -1 {
				xPos = float64(*xfloat)
			} else if xPos < float64(*xfloat) {
				numTabs := howManyTabs(xPos, float64(*xfloat))
				txt += strings.Repeat("\t", numTabs)
				xPos = float64(*xfloat)
			}
		}

		if op.Operand == "Td" || op.Operand == "TD" || op.Operand == "T*" {
			// Move to next line...
			txt += "\n"
		}
		if inText && op.Operand == "TJ" {
			if len(op.Params) < 1 {
				continue
			}
			paramList, ok := op.Params[0].(*core.PdfObjectArray)
			if !ok {
				return "", fmt.Errorf("Invalid parameter type, no array (%T)", op.Params[0])
			}
			for _, obj := range *paramList {
				switch v := obj.(type) {
				case *core.PdfObjectString:
					txt += string(*v)
				case *core.PdfObjectFloat:
					if *v < -100 {
						txt += " "
					}
				case *core.PdfObjectInteger:
					if *v < -100 {
						txt += " "
					}
				}
			}
		} else if inText && op.Operand == "Tj" {
			if len(op.Params) < 1 {
				continue
			}
			param, ok := op.Params[0].(*core.PdfObjectString)
			if !ok {
				return "", fmt.Errorf("Invalid parameter type, not string (%T)", op.Params[0])
			}
			txt += string(*param)
		}
	}

	return txt, nil
}

func extractPDFText(r io.ReadSeeker) ([]string, error) {
	pdfReader, err := pdf.NewPdfReader(r)
	if err != nil {
		return nil, err
	}

	numPages, err := pdfReader.GetNumPages()
	if err != nil {
		return nil, err
	}
	pages := make([]string, numPages)
	decoder := charmap.Windows1252.NewDecoder()
	for i := 1; i <= numPages; i++ {
		page, err := pdfReader.GetPage(i)
		if err != nil {
			return nil, err
		}
		contentStreams, err := page.GetContentStreams()
		if err != nil {
			return nil, err
		}
		pageContentStr := ""

		// If the value is an array, the effect shall be as if all of the
		// streams in the array were concatenated, in order, to form a
		// single stream.
		for _, cstream := range contentStreams {
			pageContentStr += cstream + "\n"
		}

		cstreamParser := pdfcontent.NewContentStreamParser(pageContentStr)
		txt, err := extractText(cstreamParser)
		if err != nil {
			return nil, err
		}
		s, err := decoder.String(txt)
		if err != nil {
			fmt.Printf("Error decoding stream: %q\n", txt)
			return nil, err
		}
		pages[i-1] = strings.TrimSpace(s)
	}
	return pages, nil
}

func parseLine(text string) ([]string, error) {
	parts := strings.Split(text, "\t")
	if len(parts) == 8 && parts[1] == "" && parts[2] == "" && parts[3] == "" && strings.Contains(parts[7], "Page ") {
		// page number on first PDF
		return nil, io.EOF
	}
	if len(parts) < 8 && strings.Contains(parts[0], "If there is a discrepancy in the listing of the card balance") {
		return nil, io.EOF
	}
	if len(parts) != 8 {
		return nil, fmt.Errorf("invalid line: %q", text)
	}
	return parts, nil
}

var recordHeader = []string{
	"Date",
	"Transaction Type",
	"Location",
	"Route",
	"Product",
	"Debit",
	"Credit",
	"Balance",
}

func getCSV(pages []string) (int64, [][]string, error) {
	records := make([][]string, 1)
	records[0] = make([]string, 8)
	copy(records[0][:], recordHeader)
	num := int64(-1)
	for i := range pages {
		if i == 0 {
			bs := bufio.NewScanner(strings.NewReader(pages[i]))
			line := 0
			for bs.Scan() {
				text := bs.Text()
				if line == 0 {
					if text != "TRANSACTION HISTORY FOR" {
						rest.Logger.Warn("Unexpected line text", "line", line, "text", text)
					}
					line++
					continue
				}
				if line == 1 {
					if !strings.HasPrefix(text, "CARD ") {
						rest.Logger.Warn("Unexpected line text", "line", line, "text", text)
						line++
						continue
					}
					parts := strings.Split(text, " ")
					if len(parts) != 2 {
						rest.Logger.Warn("Unexpected line text", "line", line, "text", text)
						line++
						continue
					}
					var err error
					num, err = strconv.ParseInt(parts[1], 10, 64)
					if err != nil {
						rest.Logger.Warn("error reading account number", "line", line, "text", text)
					}
					line++
					continue
				}
				if line == 2 {
					if !strings.HasPrefix(text, "TRANSACTION TYPE\tLOCATION\tROUTE") {
						rest.Logger.Warn("Unexpected line text", "line", line, "text", text)
					}
					line++
					continue
				}
				parts, err := parseLine(text)
				if err == io.EOF {
					break
				}
				if err != nil {
					return num, nil, err
				}
				records = append(records, parts)
				line++
			}
		} else {
			bs := bufio.NewScanner(strings.NewReader(pages[i]))
			line := 0
			for bs.Scan() {
				text := bs.Text()
				if line == 0 {
					if !strings.HasPrefix(text, "TRANSACTION TYPE\tLOCATION\tROUTE") {
						rest.Logger.Warn("Unexpected line text", "line", line, "text", text)
					}
					line++
					continue
				}
				parts, err := parseLine(text)
				if err == io.EOF {
					break
				}
				if err != nil {
					return num, nil, err
				}
				records = append(records, parts)
				line++
			}
		}
	}
	return num, records, nil
}

type TransactionData struct {
	AccountNumber int64
	Transactions  [][]string
}

// ParsePDF parses r (a stream of PDF encoded data) and returns a list of
// transaction records suitable for encoding in a CSV file.
//
// Each row in the output will have 8 columns. Note, the transaction data in the
// PDF is not well validated; as long as it has 8 columns (or close to it), the
// file will be returned as is.
func ParsePDF(r io.ReadSeeker) (TransactionData, error) {
	pages, err := extractPDFText(r)
	if err != nil {
		return TransactionData{}, err
	}
	accountNumber, records, err := getCSV(pages)
	if err != nil {
		return TransactionData{}, err
	}
	return TransactionData{
		AccountNumber: accountNumber,
		Transactions:  records,
	}, nil
}

type Card struct {
	Nickname            string
	SerialNumber        int64
	Status              string
	Reason              string
	Type                string
	CashValueCents      int
	AutoloadAmountCents int
}

type Client struct {
	username, password string
	client             *http.Client

	loggedIn bool
	mu       sync.Mutex
}

func NewClient(username, password string) (*Client, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Jar:       jar,
		Transport: rest.DefaultTransport,
	}
	return &Client{
		username: username,
		password: password,
		client:   client,
	}, nil
}

const host = "https://www.clippercard.com"
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.13; rv:58.0) Gecko/20100101 Firefox/58.0"

func (c *Client) Cards(ctx context.Context) ([]Card, error) {
	cards, err := c.cards(ctx)
	return cards, err
}

// caller should hold c.mu
func (c *Client) login(ctx context.Context) (*http.Response, error) {
	// First, get the login page to obtain CSRF token
	req, err := http.NewRequest("GET", host+"/ClipperWeb/login.html", nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("could not get Clipper login page: want 200 response code, got %d", resp.StatusCode)
	}
	
	// Extract CSRF token from the page
	csrfToken, err := findCSRFToken(resp.Body)
	if err != nil {
		return nil, err
	}
	_, discardErr := io.Copy(ioutil.Discard, resp.Body)
	if discardErr != nil {
		return nil, discardErr
	}
	closeErr := resp.Body.Close()
	if closeErr != nil {
		return nil, closeErr
	}

	// Now submit the login form
	data := url.Values{}
	data.Set("_csrf", csrfToken)
	data.Set("email", c.username)
	data.Set("password", c.password)

	req, err = http.NewRequest("POST", host+"/ClipperWeb/account", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", "https://www.clippercard.com/ClipperWeb/login.html")
	resp2, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp2.StatusCode != 200 && resp2.StatusCode != 302 {
		return nil, fmt.Errorf("could not login: want 200 or 302 response code, got %d", resp2.StatusCode)
	}
	c.loggedIn = true
	return resp2, nil
}

func (c *Client) dashboard(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequest("GET", host+"/ClipperWeb/account.html", nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("could not get dashboard: want 200 response code, got %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) cards(ctx context.Context) ([]Card, error) {
	var resp *http.Response
	var err error
	c.mu.Lock()
	if c.loggedIn {
		c.mu.Unlock()
		resp, err = c.dashboard(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		resp, err = c.login(ctx)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.mu.Unlock()
	}
	dashboardData, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := resp.Body.Close(); err != nil {
		return nil, err
	}
	cards, err := getCards(bytes.NewReader(dashboardData))
	return cards, err
}

func (c *Client) Transactions(ctx context.Context) (map[Card]TransactionData, error) {
	// TODO: Update this method to work with new ClipperWeb API
	return nil, fmt.Errorf("Transactions method not yet updated for new ClipperWeb API - use DownloadPDFs instead")
}

// DownloadPDFs downloads raw PDF transaction reports and saves them to the specified directory
// startDate and endDate should be in YYYY-MM-DD format, or empty for default range
// Set dryRun to true to test without actually downloading PDFs
func (c *Client) DownloadPDFs(ctx context.Context, outputDir string, startDate, endDate string, dryRun bool) error {
	cards, err := c.cards(ctx)
	if err != nil {
		return err
	}
	
	// Get CSRF token from account page
	req, err := http.NewRequest("GET", host+"/ClipperWeb/account.html", nil)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("could not get account page: want 200 response code, got %d", resp.StatusCode)
	}
	
	csrfToken, err := findCSRFToken(resp.Body)
	if err != nil {
		return err
	}
	_, discardErr := io.Copy(ioutil.Discard, resp.Body)
	if discardErr != nil {
		return discardErr
	}
	closeErr := resp.Body.Close()
	if closeErr != nil {
		return closeErr
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	
	for _, card := range cards {
		// Create form data for PDF download
		data := url.Values{}
		data.Set("_csrf", csrfToken)
		data.Set("cardNumber", strconv.FormatInt(card.SerialNumber, 10))
		data.Set("cardNickName", card.Nickname)
		data.Set("rhStartDate", "")
		data.Set("rhEndDate", "")
		// Set date range
		if startDate != "" {
			data.Set("startDateValue", startDate)
			data.Set("startDate", startDate)
		} else {
			data.Set("startDateValue", "")
			data.Set("startDate", "")
		}
		if endDate != "" {
			data.Set("endDateValue", endDate)
			data.Set("endDate", endDate)
		} else {
			data.Set("endDateValue", "")
			data.Set("endDate", "")
		}
		
		if dryRun {
			fmt.Printf("[DRY RUN] Would download PDF for card %d (%s) with date range: %s to %s\n", 
				card.SerialNumber, card.Nickname, 
				map[bool]string{true: startDate, false: "default"}[startDate != ""], 
				map[bool]string{true: endDate, false: "default"}[endDate != ""])
			continue
		}
		
		req, err := http.NewRequest("POST", host+"/ClipperWeb/view/transactionHistory.pdf", strings.NewReader(data.Encode()))
		if err != nil {
			return err
		}
		req = req.WithContext(ctx)
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/pdf,*/*")
		req.Header.Set("Referer", "https://www.clippercard.com/ClipperWeb/account.html")
		
		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("bad status for card %d: want 200 got %d", card.SerialNumber, resp.StatusCode)
		}
		
		ctype := resp.Header.Get("Content-Type")
		typ, _, err := mime.ParseMediaType(ctype)
		if err != nil {
			return err
		}
		if typ != "application/pdf" {
			return fmt.Errorf("could not get transactions for card %d: Bad response content-type: want pdf got %s", card.SerialNumber, ctype)
		}
		
		pdfBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := resp.Body.Close(); err != nil {
			return err
		}
		
		// Save raw PDF to file
		filename := fmt.Sprintf("%s/clipper-transactions-%d.pdf", outputDir, card.SerialNumber)
		err = ioutil.WriteFile(filename, pdfBody, 0644)
		if err != nil {
			return err
		}
		fmt.Printf("Saved PDF: %s (Card: %s)\n", filename, card.Nickname)
	}
	return nil
}
