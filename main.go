package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-github/v56/github"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

const (
	// Default values
	defaultDays = 30

	// Date format for GitHub API
	dateFormat = "2006-01-02"

	// Progress bar and pagination settings
	perPageLimit = 100

	defaultPrompt = `An employee is undergoing a performance review. They have contributed to the company by merging several pull requests.
Describe their major contributions based on the PR descriptions in @%s. Be sure to emphasize the impact of their work and any significant features or improvements they introduced.
Include links to PRs. Don't write any files.`
)

// Config holds the complete application configuration
type Config struct {
	Username    string   `yaml:"username"`
	Since       string   `yaml:"since,omitempty"`
	Until       string   `yaml:"until,omitempty"`
	Days        int      `yaml:"days,omitempty"`
	OutputDir   string   `yaml:"output_dir"`
	ExtraPrompt string   `yaml:"extra-prompt,omitempty"`
	Repos       []string `yaml:"repos"`

	// Parsed fields (not in YAML)
	SinceTime time.Time `yaml:"-"`
	UntilTime time.Time `yaml:"-"`
	ReposNWO  []NWO     `yaml:"-"`
}

type NWO struct {
	Owner string
	Name  string
}

// Parse validates and parses the configuration
func (c *Config) Parse() error {
	// Validate required fields
	if c.Username == "" {
		return fmt.Errorf("username is required")
	}
	if c.OutputDir == "" {
		return fmt.Errorf("output_dir is required")
	}
	if len(c.Repos) == 0 {
		return fmt.Errorf("repos list cannot be empty")
	}

	// Set default days if not specified
	if c.Days == 0 && c.Since == "" && c.Until == "" {
		c.Days = defaultDays
	}

	// Parse repositories
	var repos []NWO
	for _, repoStr := range c.Repos {
		parts := strings.Split(strings.TrimSpace(repoStr), "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid repository format '%s': expected 'owner/name'", repoStr)
		}

		owner := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])

		if owner == "" || name == "" {
			return fmt.Errorf("invalid repository format '%s': owner and name cannot be empty", repoStr)
		}

		repos = append(repos, NWO{
			Owner: owner,
			Name:  name,
		})
	}
	c.ReposNWO = repos

	// Parse dates
	var err error
	if c.Since != "" && c.Until != "" {
		c.SinceTime, err = time.Parse(dateFormat, c.Since)
		if err != nil {
			return fmt.Errorf("invalid since date format '%s': %w", c.Since, err)
		}
		c.UntilTime, err = time.Parse(dateFormat, c.Until)
		if err != nil {
			return fmt.Errorf("invalid until date format '%s': %w", c.Until, err)
		}
	} else {
		// Use days parameter
		c.UntilTime = time.Now()
		c.SinceTime = c.UntilTime.AddDate(0, 0, -c.Days)
	}

	return nil
}

// PullRequestInfo holds the information we want to display about PRs
type PullRequestInfo struct {
	Repository  string
	Title       string
	Description string
	URL         string
	CreatedAt   time.Time
	MergedAt    *time.Time
}

// loadConfig loads configuration from a YAML file
func loadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	var config Config

	// Create a decoder with strict mode to reject unknown fields
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)

	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	// Parse and validate the configuration
	if err := config.Parse(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return &config, nil
}

// confirmOverwrite checks if a file exists and asks user for confirmation to overwrite
// Returns true if file should be written (either doesn't exist or user confirmed overwrite)
func confirmOverwrite(filePath string) (bool, error) {
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, should write it
			return true, nil
		}
		return false, fmt.Errorf("error checking file %s: %w", filePath, err)
	}

	// File exists, ask for confirmation
	fmt.Printf("File %s already exists. Do you want to overwrite it? (y/N): ", filePath)
	var response string
	fmt.Scanln(&response)

	response = strings.ToLower(strings.TrimSpace(response))
	if response == "y" || response == "yes" {
		return true, nil
	}

	// User chose not to overwrite
	return false, nil
}

func main() {
	// Parse command line arguments
	var (
		configFile = flag.String("config", "config.yaml", "Path to configuration file")
	)
	flag.Parse()

	// Load configuration from file
	config, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory %s: %v", config.OutputDir, err)
	}

	// Check for existing output files and confirm overwrite BEFORE doing expensive work
	prsFile := filepath.Join(config.OutputDir, "prs.md")
	summaryFile := filepath.Join(config.OutputDir, "summary.md")

	// Check summary file first - if user doesn't want to generate new summary, exit early
	shouldWriteSummary, err := confirmOverwrite(summaryFile)
	if err != nil {
		log.Fatalf("Cannot check summary file: %v", err)
	}

	if !shouldWriteSummary {
		log.Printf("Summary file %s already exists and user chose not to overwrite. Nothing to do.", summaryFile)
		return
	}

	// Now check PR file since we know we'll need it for summary generation
	shouldWritePRs, err := confirmOverwrite(prsFile)
	if err != nil {
		log.Fatalf("Cannot check PR file: %v", err)
	}

	// Only fetch PRs if we need to write the PR file
	if shouldWritePRs {
		// Get GitHub token using gh CLI
		token, err := getGitHubToken()
		if err != nil {
			log.Fatalf("Failed to get GitHub token: %v", err)
		}

		// Create GitHub client
		ctx := context.Background()
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		tc := oauth2.NewClient(ctx, ts)
		client := github.NewClient(tc)

		// Count total PRs across all repositories
		log.Printf("Counting PRs across %d repositories...", len(config.ReposNWO))
		totalPRs := 0
		for _, repo := range config.ReposNWO {
			count, err := countMergedPRs(ctx, client, repo, *config)
			if err != nil {
				log.Printf("Warning: Error counting PRs from %s/%s: %v", repo.Owner, repo.Name, err)
				continue
			}
			totalPRs += count
		}

		if totalPRs == 0 {
			log.Printf("No merged PRs found in the specified time range.")
			return
		}

		log.Printf("Found %d PRs to process.", totalPRs)

		// Create progress bar for individual PRs
		bar := progressbar.NewOptions(totalPRs,
			progressbar.OptionSetDescription("Processing PRs"),
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionShowCount(),
			progressbar.OptionShowIts(),
			progressbar.OptionSetPredictTime(true),
			progressbar.OptionFullWidth(),
			progressbar.OptionSetRenderBlankState(true),
		)

		// Retrieve PRs for each repository with progress tracking
		var allPRs []PullRequestInfo
		for _, repo := range config.ReposNWO {
			prs, err := getMergedPRsWithProgress(ctx, client, repo, *config, bar)
			if err != nil {
				log.Printf("Error fetching PRs from %s/%s: %v", repo.Owner, repo.Name, err)
				continue
			}
			allPRs = append(allPRs, prs...)
		}

		bar.Finish()
		log.Printf("Completed processing %d merged PRs", len(allPRs))

		// Write PR descriptions to the output directory
		log.Printf("Writing PR descriptions to %s", prsFile)
		if err := outputPRs(allPRs, prsFile); err != nil {
			log.Fatalf("Error writing PR descriptions to output file: %v", err)
		}
	} else {
		log.Printf("Using existing PR descriptions from %s", prsFile)
	}

	// Use copilot CLI to summarize the content
	log.Printf("Generating summary with Copilot...")
	summary, err := generateSummaryWithCopilot(prsFile, config.ExtraPrompt)
	if err != nil {
		log.Fatalf("Error generating summary: %v", err)
	}

	// Write summary to final output
	if err := writeSummaryToOutput(summary, summaryFile); err != nil {
		log.Fatalf("Error writing summary: %v", err)
	}
}

// getGitHubToken retrieves the GitHub token using the gh CLI
func getGitHubToken() (string, error) {
	cmd := exec.Command("gh", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("failed to get token from gh CLI: %w\nStderr: %s\nMake sure you're logged in with 'gh auth login'", err, string(exitError.Stderr))
		}
		return "", fmt.Errorf("failed to get token from gh CLI: %w (make sure you're logged in with 'gh auth login')", err)
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("empty token received from gh CLI")
	}

	return token, nil
}

// buildSearchQuery creates a search query for GitHub API
func buildSearchQuery(repo NWO, config Config) string {
	query := fmt.Sprintf("repo:%s/%s is:pr is:merged author:%s created:%s..%s",
		repo.Owner, repo.Name, config.Username,
		config.SinceTime.Format(dateFormat), config.UntilTime.Format(dateFormat))

	log.Printf("GitHub search query for %s/%s: %s", repo.Owner, repo.Name, query)
	return query
}

// countMergedPRs counts the number of merged PRs for a repository without fetching full details
func countMergedPRs(ctx context.Context, client *github.Client, repo NWO, config Config) (int, error) {
	query := buildSearchQuery(repo, config)

	opts := &github.SearchOptions{
		Sort:  "created",
		Order: "desc",
		ListOptions: github.ListOptions{
			PerPage: 1, // We only need the count, not the actual results
		},
	}

	result, _, err := client.Search.Issues(ctx, query, opts)
	if err != nil {
		return 0, fmt.Errorf("failed to count PRs: %w", err)
	}

	return result.GetTotal(), nil
}

// getMergedPRsWithProgress retrieves merged PRs for a specific repository with progress tracking
func getMergedPRsWithProgress(ctx context.Context, client *github.Client, repo NWO, config Config, bar *progressbar.ProgressBar) ([]PullRequestInfo, error) {
	var allPRs []PullRequestInfo

	query := buildSearchQuery(repo, config)

	opts := &github.SearchOptions{
		Sort:  "created",
		Order: "desc",
		ListOptions: github.ListOptions{
			PerPage: perPageLimit,
		},
	}

	for {
		result, resp, err := client.Search.Issues(ctx, query, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to search PRs: %w", err)
		}

		for _, issue := range result.Issues {
			if bar != nil {
				bar.Describe(fmt.Sprintf("Processing PR #%d from %s/%s", issue.GetNumber(), repo.Owner, repo.Name))
			}

			// Convert GitHub issue to our PR info structure
			prInfo := PullRequestInfo{
				Repository:  fmt.Sprintf("%s/%s", repo.Owner, repo.Name),
				Title:       issue.GetTitle(),
				Description: issue.GetBody(),
				URL:         issue.GetHTMLURL(),
				CreatedAt:   issue.GetCreatedAt().Time,
			}

			// Get the actual PR to get merge information and full description
			pr, _, err := client.PullRequests.Get(ctx, repo.Owner, repo.Name, issue.GetNumber())
			if err != nil {
				log.Printf("Warning: failed to get PR details for #%d: %v", issue.GetNumber(), err)
			} else {
				// Update description with PR body if available (more detailed than issue body)
				if pr.GetBody() != "" {
					prInfo.Description = pr.GetBody()
				}
				// Set merge time if available
				if pr.MergedAt != nil {
					mergedAt := pr.GetMergedAt().Time
					prInfo.MergedAt = &mergedAt
				}
			}

			allPRs = append(allPRs, prInfo)
			if bar != nil {
				bar.Add(1)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allPRs, nil
}

// getOutputWriter returns the appropriate writer for the given output file
func getOutputWriter(outputFile string) (*os.File, error) {
	if outputFile != "" {
		writer, err := os.Create(outputFile)
		if err != nil {
			return nil, fmt.Errorf("failed to create output file %s: %w", outputFile, err)
		}
		return writer, nil
	}
	return os.Stdout, nil
}

// outputPRs outputs the PR information as Markdown
func outputPRs(prs []PullRequestInfo, outputFile string) error {
	writer, err := getOutputWriter(outputFile)
	if err != nil {
		return err
	}
	if outputFile != "" {
		defer writer.Close()
		log.Printf("Writing PR details to %s", outputFile)
	}

	// Write markdown header
	fmt.Fprintf(writer, "# Merged Pull Requests\n\n")
	fmt.Fprintf(writer, "Found %d merged pull requests.\n\n", len(prs))

	if len(prs) == 0 {
		fmt.Fprintf(writer, "*No merged PRs found.*\n")
		return nil
	}

	// Group PRs by repository
	repoGroups := make(map[string][]PullRequestInfo)
	for _, pr := range prs {
		repoGroups[pr.Repository] = append(repoGroups[pr.Repository], pr)
	}

	// Output each repository group
	for repo, repoPRs := range repoGroups {
		fmt.Fprintf(writer, "## %s\n\n", repo)

		for _, pr := range repoPRs {
			// PR title as H3 with link
			fmt.Fprintf(writer, "### [%s](%s)\n\n", pr.Title, pr.URL)

			// Metadata table
			fmt.Fprintf(writer, "| Field | Value |\n")
			fmt.Fprintf(writer, "|-------|-------|\n")
			fmt.Fprintf(writer, "| **Created** | %s |\n", pr.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(writer, "| **Link** | <%s> |\n", pr.URL)

			if pr.MergedAt != nil {
				fmt.Fprintf(writer, "| **Merged** | %s |\n", pr.MergedAt.Format("2006-01-02 15:04:05"))
			} else {
				fmt.Fprintf(writer, "| **Merged** | *Not available* |\n")
			}

			fmt.Fprintf(writer, "\n")

			// PR description - extract appropriate description based on repository
			if strings.TrimSpace(pr.Description) != "" {
				fmt.Fprintf(writer, "#### Description\n\n")

				descriptionText := getRepositorySpecificDescription(pr.Repository, pr.Description)
				fmt.Fprintf(writer, "%s\n\n", descriptionText)
			} else {
				fmt.Fprintf(writer, "#### Description\n\n*No description provided.*\n\n")
			}

			// Separator between PRs
			fmt.Fprintf(writer, "---\n\n")
		}
	}

	return nil
}

// filterHTMLComments removes HTML comments from the given text while preserving line structure
func filterHTMLComments(text string) string {
	lines := strings.Split(text, "\n")
	var cleanLines []string
	inComment := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check for comment start and end on the same line
		if strings.HasPrefix(trimmedLine, "<!--") && strings.HasSuffix(trimmedLine, "-->") {
			continue // Skip single-line comments
		}

		// Check for comment start
		if strings.HasPrefix(trimmedLine, "<!--") {
			inComment = true
			continue
		}

		// Check for comment end
		if strings.HasSuffix(trimmedLine, "-->") {
			inComment = false
			continue
		}

		// Skip lines inside comments
		if inComment {
			continue
		}

		cleanLines = append(cleanLines, line)
	}

	return strings.Join(cleanLines, "\n")
}

// filterHTMLCommentsAndEmptyLinesAtStart removes HTML comments and empty lines from the start of content
func filterHTMLCommentsAndEmptyLinesAtStart(lines []string) []string {
	var contentLines []string

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Skip HTML comments and empty lines at the start
		if len(contentLines) == 0 {
			if trimmedLine == "" || strings.HasPrefix(trimmedLine, "<!--") || strings.HasSuffix(trimmedLine, "-->") {
				continue
			}
		}

		contentLines = append(contentLines, line)
	}

	return contentLines
}

// extractDescriptionForTSS extracts only the first section from a PR description
// that follows the standard template format
func extractDescriptionForTSS(description string) string {
	lines := strings.Split(description, "\n")
	var firstSection []string
	inFirstSection := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if this is the start of the first section
		if strings.HasPrefix(trimmedLine, "### What are you trying to accomplish?") {
			inFirstSection = true
			continue // Skip the section header itself
		}

		// Check if we've hit another section header (starts with ###)
		if inFirstSection && strings.HasPrefix(trimmedLine, "###") {
			break // Stop at the next section
		}

		// If we're in the first section, collect the content
		if inFirstSection {
			firstSection = append(firstSection, line)
		}
	}

	// Join the lines and clean up
	result := strings.Join(firstSection, "\n")
	result = strings.TrimSpace(result)

	// If we didn't find the standard format, return the original description
	if result == "" {
		return description
	}

	return result
}

func extractDescriptionForDotcom(description string) string {
	// First, try to extract content from "### What are you trying to accomplish?" section
	accomplishMarker := "### What are you trying to accomplish?"
	accomplishIndex := strings.Index(description, accomplishMarker)

	if accomplishIndex != -1 {
		// Find the start of the content after the marker
		contentStart := accomplishIndex + len(accomplishMarker)
		remainingContent := description[contentStart:]

		// Split into lines and find the actual content (skip empty lines and comments)
		lines := strings.Split(remainingContent, "\n")
		var contentLines []string

		for _, line := range lines {
			trimmedLine := strings.TrimSpace(line)

			// Stop if we hit another section header
			if strings.HasPrefix(trimmedLine, "###") {
				break
			}

			contentLines = append(contentLines, line)
		}

		// Filter HTML comments and empty lines at start, then trim
		contentLines = filterHTMLCommentsAndEmptyLinesAtStart(contentLines)
		extractedContent := strings.Join(contentLines, "\n")
		extractedContent = strings.TrimSpace(extractedContent)

		// If we found non-empty content, return it
		if extractedContent != "" {
			return extractedContent
		}
	}

	// Fallback: Look for the "### What approach did you choose and why?" section and truncate there
	approachMarker := "### What approach did you choose and why?"
	index := strings.Index(description, approachMarker)

	var contentToProcess string
	if index == -1 {
		contentToProcess = description
	} else {
		// Extract everything before the marker
		contentToProcess = description[:index]
	}

	// Filter out HTML comments and clean up the content
	result := filterHTMLComments(contentToProcess)
	return strings.TrimSpace(result)
}

// getRepositorySpecificDescription returns the appropriate description text based on the repository
func getRepositorySpecificDescription(repository, description string) string {
	switch repository {
	case "github/token-scanning-service":
		return extractDescriptionForTSS(description)
	case "github/github":
		return extractDescriptionForDotcom(description)
	default:
		// Use full description for other repositories
		return description
	}
}

// generateSummaryWithCopilot uses the copilot CLI to generate a summary of the PR descriptions
func generateSummaryWithCopilot(prsFilePath, extraPrompt string) (string, error) {
	// Get the directory containing the prs file and the filename
	prsDir, err := filepath.Abs(filepath.Dir(prsFilePath))
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for directory: %w", err)
	}
	prsFileName := filepath.Base(prsFilePath)

	// Build the prompt starting with the default, using just the filename
	prompt := fmt.Sprintf(defaultPrompt, prsFileName)

	// Add custom instructions if provided
	if extraPrompt != "" {
		// Append additional instructions to the default prompt
		prompt = fmt.Sprintf("%s\n\nAdditional instructions:\n%s", prompt, strings.TrimSpace(extraPrompt))
	}

	log.Printf("Copilot prompt: %s", prompt)

	// Use copilot CLI with the directory added and reference the filename in the prompt
	cmd := exec.Command("copilot", "--disable-builtin-mcps", "--deny-tool", "--no-color", "--no-custom-instructions", "--add-dir", prsDir, "-p", prompt)
	cmd.Dir = prsDir

	output, err := cmd.Output()
	if err != nil {
		// If there's an error, try to get stderr for more details
		if exitError, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("failed to run copilot CLI: %w\nStderr: %s", err, string(exitError.Stderr))
		}
		return "", fmt.Errorf("failed to run copilot CLI: %w (make sure copilot CLI is installed and available)", err)
	}

	summary := strings.TrimSpace(string(output))
	if summary == "" {
		return "", fmt.Errorf("copilot CLI returned empty summary")
	}

	return summary, nil
}

// writeSummaryToOutput writes the summary to the specified output file or stdout
func writeSummaryToOutput(summary, outputFile string) error {
	writer, err := getOutputWriter(outputFile)
	if err != nil {
		return err
	}
	if outputFile != "" {
		defer writer.Close()
		log.Printf("Writing summary to %s", outputFile)
	}

	// Write the summary
	fmt.Fprintf(writer, "# PR Summary\n\n")
	fmt.Fprintf(writer, "%s\n", summary)

	return nil
}
