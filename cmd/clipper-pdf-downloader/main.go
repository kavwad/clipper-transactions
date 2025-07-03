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

func main() {
	flag.Parse()
	if *email == "" || *password == "" {
		fmt.Fprintf(os.Stderr, "Please provide an email and a password\n")
		fmt.Fprintf(os.Stderr, "Usage: %s -email=your@email.com -password=yourpassword [-output=./pdfs]\n", os.Args[0])
		os.Exit(2)
	}

	client, err := clipper.NewClient(*email, *password)
	checkError(err, "creating client")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	fmt.Println("Downloading PDF transaction reports...")
	
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		checkError(err, "creating output directory")
	}
	
	// Download raw PDFs
	err = client.DownloadPDFs(ctx, *outputDir)
	checkError(err, "downloading PDFs")

	fmt.Printf("PDF downloads completed. Files saved to: %s\n", *outputDir)
}