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

var email = flag.String("email", "", "Login email (optional if using --user or --all)")
var password = flag.String("password", "", "Password (optional if using --user or --all)")
var outputDir = flag.String("output", "pdfs", "Output directory for PDF files")
var startDate = flag.String("start", "", "Start date for transaction range (YYYY-MM-DD format, optional)")
var endDate = flag.String("end", "", "End date for transaction range (YYYY-MM-DD format, optional)")
var lastMonth = flag.Bool("last-month", false, "Download last month's transactions (overrides start/end dates)")
var dryRun = flag.Bool("dry-run", false, "Test run without downloading PDFs (avoids API limits)")
var user = flag.String("user", "", "Username from config file (e.g., --user=kaveh)")
var all = flag.Bool("all", false, "Download for all users in config file")
var configFile = flag.String("config", "config.yml", "Path to config file")

func main() {
	flag.Parse()
	// Determine which users to process
	var usersToProcess []struct {
		name     string
		email    string
		password string
	}
	
	// Check for conflicting flags
	if (*user != "" && *all) || (*user != "" && (*email != "" || *password != "")) || (*all && (*email != "" || *password != "")) {
		fmt.Fprintf(os.Stderr, "Cannot use --user, --all, and manual credentials together\n")
		os.Exit(2)
	}
	
	// Default to --all if no specific user method is specified
	if *user == "" && !*all && *email == "" && *password == "" {
		*all = true
	}
	
	if *user != "" || *all {
		config, err := loadConfig(*configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file %s: %v\n", *configFile, err)
			fmt.Fprintf(os.Stderr, "Make sure to copy config.example.yml to config.yml and fill in your credentials\n")
			os.Exit(2)
		}
		
		if *user != "" {
			userData, exists := config.Users[*user]
			if !exists {
				fmt.Fprintf(os.Stderr, "User '%s' not found in config file\n", *user)
				fmt.Fprintf(os.Stderr, "Available users: ")
				for userName := range config.Users {
					fmt.Fprintf(os.Stderr, "%s ", userName)
				}
				fmt.Fprintf(os.Stderr, "\n")
				os.Exit(2)
			}
			usersToProcess = append(usersToProcess, struct {
				name     string
				email    string
				password string
			}{*user, userData.Email, userData.Password})
		} else {
			// Process all users
			for userName, userData := range config.Users {
				usersToProcess = append(usersToProcess, struct {
					name     string
					email    string
					password string
				}{userName, userData.Email, userData.Password})
			}
		}
	} else {
		// Use manual credentials
		if *email == "" || *password == "" {
			fmt.Fprintf(os.Stderr, "Please provide credentials\n")
			fmt.Fprintf(os.Stderr, "Usage: %s [--user=username] [--all] OR [--email=email --password=password] [options]\n", os.Args[0])
			fmt.Fprintf(os.Stderr, "Options: [--output=./pdfs] [--start=2024-01-01] [--end=2024-01-31] [--last-month] [--dry-run]\n")
			os.Exit(2)
		}
		usersToProcess = append(usersToProcess, struct {
			name     string
			email    string
			password string
		}{"manual", *email, *password})
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute) // Longer timeout for multiple users
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
	
	// Process each user
	for i, userInfo := range usersToProcess {
		if len(usersToProcess) > 1 {
			fmt.Printf("\n=== Processing user: %s (%s) ===\n", userInfo.name, userInfo.email)
		}
		
		client, err := clipper.NewClient(userInfo.email, userInfo.password)
		checkError(err, fmt.Sprintf("creating client for user %s", userInfo.name))
		
		// Download raw PDFs (or dry run)
		err = client.DownloadPDFs(ctx, *outputDir, finalStartDate, finalEndDate, *dryRun)
		checkError(err, fmt.Sprintf("downloading PDFs for user %s", userInfo.name))
		
		if i < len(usersToProcess)-1 {
			fmt.Println("Waiting 2 seconds before next user...")
			time.Sleep(2 * time.Second)
		}
	}

	if *dryRun {
		fmt.Println("\n[DRY RUN] Test completed successfully.")
	} else {
		fmt.Printf("\nPDF downloads completed for %d user(s). Files saved to: %s\n", len(usersToProcess), *outputDir)
	}
}