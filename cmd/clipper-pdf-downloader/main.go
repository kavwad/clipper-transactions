package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kevinburke/clipper"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Users map[string]struct {
		Email    string `yaml:"email"`
		Password string `yaml:"password"`
	} `yaml:"users"`
}

func checkError(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error %s: %v\n", msg, err)
		os.Exit(2)
	}
}

func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var config Config
	err = yaml.Unmarshal(data, &config)
	return &config, err
}

var email = flag.String("email", "", "Login email (optional if using -k or -b)")
var password = flag.String("password", "", "Password (optional if using -k or -b)")
var outputDir = flag.String("output", "pdfs", "Output directory for PDF files")
var startDate = flag.String("start", "", "Start date for transaction range (YYYY-MM-DD format, optional)")
var endDate = flag.String("end", "", "End date for transaction range (YYYY-MM-DD format, optional)")
var lastMonth = flag.Bool("last-month", false, "Download last month's transactions (overrides start/end dates)")
var dryRun = flag.Bool("dry-run", false, "Test run without downloading PDFs (avoids API limits)")
var kaveh = flag.Bool("k", false, "Use Kaveh's credentials from config.yml")
var wife = flag.Bool("b", false, "Use wife's credentials from config.yml")
var configFile = flag.String("config", "config.yml", "Path to config file")

func main() {
	flag.Parse()
	// Determine which credentials to use
	finalEmail := *email
	finalPassword := *password
	
	if *kaveh || *wife {
		if *kaveh && *wife {
			fmt.Fprintf(os.Stderr, "Cannot use both -k and -b flags\n")
			os.Exit(2)
		}
		
		config, err := loadConfig(*configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file %s: %v\n", *configFile, err)
			fmt.Fprintf(os.Stderr, "Make sure to copy config.example.yml to config.yml and fill in your credentials\n")
			os.Exit(2)
		}
		
		var userKey string
		if *kaveh {
			userKey = "kaveh"
		} else {
			userKey = "wife"
		}
		
		user, exists := config.Users[userKey]
		if !exists {
			fmt.Fprintf(os.Stderr, "User '%s' not found in config file\n", userKey)
			os.Exit(2)
		}
		
		finalEmail = user.Email
		finalPassword = user.Password
	}
	
	if finalEmail == "" || finalPassword == "" {
		fmt.Fprintf(os.Stderr, "Please provide credentials either via flags or config file\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [-k|-b] OR [-email=your@email.com -password=yourpassword] [other options]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options: [-output=./pdfs] [-start=2024-01-01] [-end=2024-01-31] [-last-month] [-dry-run]\n")
		os.Exit(2)
	}

	client, err := clipper.NewClient(finalEmail, finalPassword)
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