package clipper

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

func findViewState(r io.Reader) (string, error) {
	z := html.NewTokenizer(r)
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return "", errors.New("ViewState not found")
		case html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data != "input" {
				continue
			}
			foundViewState := false
			for i := range tok.Attr {
				if tok.Attr[i].Key == "name" && tok.Attr[i].Val == "javax.faces.ViewState" {
					foundViewState = true

				}
			}
			if !foundViewState {
				continue
			}
			for i := range tok.Attr {
				if tok.Attr[i].Key == "value" {
					return tok.Attr[i].Val, nil
				}
			}
		}
	}
}

func findCSRFToken(r io.Reader) (string, error) {
	z := html.NewTokenizer(r)
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return "", errors.New("CSRF token not found")
		case html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data != "input" {
				continue
			}
			foundCSRF := false
			for i := range tok.Attr {
				if tok.Attr[i].Key == "name" && tok.Attr[i].Val == "_csrf" {
					foundCSRF = true
				}
			}
			if !foundCSRF {
				continue
			}
			for i := range tok.Attr {
				if tok.Attr[i].Key == "value" {
					return tok.Attr[i].Val, nil
				}
			}
		}
	}
}

func setNickSerialNumber(z *html.Tokenizer, card *Card) error {
	depth := 1
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return fmt.Errorf("reached document end, nothing found: %v", z.Token())
		case html.StartTagToken:
			depth++
			tok := z.Token()
			if tok.Data != "div" {
				continue
			}
			for i := range tok.Attr {
				if tok.Attr[i].Key == "class" && tok.Attr[i].Val == "infoDiv" {
					tt = z.Next()
					for tt == html.TextToken {
						tt = z.Next()
					}
					if tt != html.StartTagToken {
						return fmt.Errorf("expected start tag token, got %#v", z.Token().String())
					}
					tok = z.Token()
					depth++
					if tok.Data != "div" || len(tok.Attr) != 1 || tok.Attr[0].Key != "class" || tok.Attr[0].Val != "fieldName" {
						return fmt.Errorf("expected start tag token, got %#v", tok.String())
					}
					tt = z.Next()
					if tt != html.TextToken {
						return errors.New("expected text token")
					}
					name := z.Token().Data
					switch name {
					case "Serial Number:":
						tt = z.Next()
						if tt != html.EndTagToken {
							return fmt.Errorf("expected end tag token, got %#v", z.Token().String())
						}
						depth--
						tt = z.Next()
						for tt == html.TextToken {
							tt = z.Next()
						}
						if tt != html.StartTagToken {
							return fmt.Errorf("expected start tag token, got %#v", z.Token().String())
						}
						depth++
						tt = z.Next()
						if tt != html.TextToken {
							return errors.New("expected text token")
						}
						num, err := strconv.ParseInt(z.Token().Data, 10, 64)
						if err != nil {
							return err
						}
						card.SerialNumber = num
						continue

					case "Card Nickname:":
						tt = z.Next() // <div class="fieldData field90">
						if tt != html.EndTagToken {
							return fmt.Errorf("expected end tag token, got %#v", z.Token().String())
						}
						depth--
						tt = z.Next()
						for tt == html.TextToken {
							tt = z.Next()
						}
						if tt != html.StartTagToken {
							return fmt.Errorf("expected start tag token, got %#v", z.Token().String())
						}
						tok = z.Token()
						depth++
						if tok.Data != "div" || len(tok.Attr) != 1 || tok.Attr[0].Key != "class" || tok.Attr[0].Val != "fieldData field90" {
							return errors.New("expected fieldData field90 token")
						}
						tt = z.Next() // <span class="displayName">
						for tt == html.TextToken {
							tt = z.Next()
						}
						if tt != html.StartTagToken {
							return fmt.Errorf("expected start tag token, got %#v", z.Token().String())
						}

						tok = z.Token()
						depth++
						if tok.Data != "span" || len(tok.Attr) != 1 || tok.Attr[0].Key != "class" || tok.Attr[0].Val != "displayName" {
							return errors.New("expected span tag token")
						}
						tt = z.Next() // the actual name
						if tt == html.EndTagToken {
							// no nickname
							depth--
							continue
						}
						if tt != html.TextToken {
							return fmt.Errorf("expected text token, got %#v\n", z.Token().String())
						}
						tok = z.Token()
						card.Nickname = tok.Data
					}
				}
			}
		case html.EndTagToken:
			depth--
			if depth <= 0 {
				return nil
			}
		}
	}
}

func getCards(r io.Reader) ([]Card, error) {
	z := html.NewTokenizer(r)
	cards := make([]Card, 0)
	
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return cards, nil
		case html.StartTagToken:
			tok := z.Token()
			if tok.Data != "span" {
				continue
			}
			// Look for spans with class "d-inline-block" that contain card info
			hasClass := false
			for i := range tok.Attr {
				if tok.Attr[i].Key == "class" && tok.Attr[i].Val == "d-inline-block" {
					hasClass = true
					break
				}
			}
			if !hasClass {
				continue
			}
			
			// Get the text content of this span
			tt = z.Next()
			if tt == html.TextToken {
				text := z.Token().Data
				// Parse card number and name from "1234567890 - CardName" format
				if card := parseCardText(text); card != nil {
					cards = append(cards, *card)
				}
			}
		}
	}
}

func parseCardText(text string) *Card {
	// Parse format like "1401491737 - Guest"
	parts := strings.Split(text, " - ")
	if len(parts) != 2 {
		return nil
	}
	
	cardNumberStr := strings.TrimSpace(parts[0])
	cardName := strings.TrimSpace(parts[1])
	
	// Validate card number is numeric and reasonable length
	cardNumber, err := strconv.ParseInt(cardNumberStr, 10, 64)
	if err != nil || len(cardNumberStr) < 10 {
		return nil
	}
	
	return &Card{
		SerialNumber: cardNumber,
		Nickname:     cardName,
		Status:       "Active", // Default assumption
		Type:         "ADULT",  // Default assumption
	}
}

func setCardInfo(z *html.Tokenizer, card *Card) error {
	depth := 1
	hitSpacer := false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return fmt.Errorf("reached document end, nothing found: %v", z.Token())
		case html.StartTagToken:
			tok := z.Token()
			depth++
			if hitSpacer || tok.Data != "div" {
				continue
			}
			for i := range tok.Attr {
				if tok.Attr[i].Key == "class" && tok.Attr[i].Val == "spacer" {
					hitSpacer = true
					continue
				}
				if tok.Attr[i].Key == "class" && tok.Attr[i].Val == "infoDiv" {
					tt = z.Next()
					for tt == html.TextToken {
						tt = z.Next()
					}
					if tt != html.StartTagToken {
						return fmt.Errorf("expected start tag token, got %#v", z.Token().String())
					}
					tok = z.Token()
					depth++
					if tok.Data != "div" || len(tok.Attr) != 1 || tok.Attr[0].Key != "class" || tok.Attr[0].Val != "fieldName" {
						return fmt.Errorf("expected start tag token, got %#v", tok.String())
					}
					tt = z.Next()
					if tt != html.TextToken {
						return errors.New("expected text token")
					}
					name := z.Token().Data
					tt = z.Next()
					if tt != html.EndTagToken {
						return fmt.Errorf("expected end tag token, got %#v", z.Token().String())
					}
					depth--
					tt = z.Next()
					for tt == html.TextToken {
						tt = z.Next()
					}
					if tt != html.StartTagToken {
						return fmt.Errorf("expected start tag token, got %#v", z.Token().String())
					}
					depth++
					tt = z.Next()
					if tt != html.TextToken {
						return errors.New("expected text token")
					}
					data := z.Token().Data
					switch name {
					case "Type:":
						card.Type = data
					case "Status:":
						card.Status = data
					case "Reason:":
						card.Reason = data
					default:
						fmt.Println("unknown name", name)
					}
					continue
				}
			}
		case html.EndTagToken:
			depth--
			if depth <= 0 {
				return nil
			}
		}
	}
}
