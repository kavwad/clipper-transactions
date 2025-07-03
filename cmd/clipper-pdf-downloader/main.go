package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kevinburke/clipper"
)

func checkError(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error %s: %v\n", msg, err)
		os.Exit(2)
	}
}

var email = flag.String("email", "", "Login email")
var password = flag.String("password", "", "Password")
var outputDir = flag.String("output", ".", "Output directory for PDF files")
var startDate = flag.String("start", "", "Start date for transaction range (YYYY-MM-DD format, optional)")
var endDate = flag.String("end", "", "End date for transaction range (YYYY-MM-DD format, optional)")
var lastMonth = flag.Bool("last-month", false, "Download last month's transactions (overrides start/end dates)")
var dryRun = flag.Bool("dry-run", false, "Test run without downloading PDFs (avoids API limits)")

func main() {
	flag.Parse()
	if *email == "" || *password == "" {
		fmt.Fprintf(os.Stderr, "Please provide an email and a password\n")
		fmt.Fprintf(os.Stderr, "Usage: %s -email=your@email.com -password=yourpassword [-output=./pdfs] [-start=2024-01-01] [-end=2024-01-31] [-last-month] [-dry-run]\n", os.Args[0])
		os.Exit(2)
	}

	client, err := clipper.NewClient(*email, *password)
	checkError(err, "creating client")

	// Handle last month flag
	finalStartDate := *startDate
	finalEndDate := *endDate
	if *lastMonth {
		now := time.Now()
		firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		lastMonthEnd := firstOfThisMonth.AddDate(0, 0, -1)
		lastMonthStart := time.Date(lastMonthEnd.Year(), lastMonthEnd.Month(), 1, 0, 0, 0, 0, lastMonthEnd.Location())
		finalStartDate = lastMonthStart.Format("2006-01-02")
		finalEndDate = lastMonthEnd.Format("2006-01-02")
		fmt.Printf("Using last month date range: %s to %s\n", finalStartDate, finalEndDate)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if *dryRun {
		fmt.Println("[DRY RUN] Testing PDF download parameters...")
	} else {
		fmt.Println("Downloading PDF transaction reports...")
		// Create output directory if it doesn't exist
		if err := os.MkdirAll(*outputDir, 0755); err != nil {
			checkError(err, "creating output directory")
		}
	}
	
	// Download raw PDFs (or dry run)
	err = client.DownloadPDFs(ctx, *outputDir, finalStartDate, finalEndDate, *dryRun)
	checkError(err, "downloading PDFs")

	if *dryRun {
		fmt.Println("[DRY RUN] Test completed successfully.")
	} else {
		fmt.Printf("PDF downloads completed. Files saved to: %s\n", *outputDir)
	}
}