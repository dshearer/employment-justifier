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
)

const (
	// Default values
	defaultDays = 30

	// Date format for GitHub API
	dateFormat = "2006-01-02"

	// Progress bar and pagination settings
	perPageLimit = 100

	defaultPrompt = `An employee is undergoing a performance review. They have contributed to the company by merging several pull requests.
Make identify their major contributions based on the PR descriptions in @%s. Be sure to emphasize the impact of their work and any significant features or improvements they introduced.
Include links to PRs.`
)

// Config holds the configuration for the PR retrieval
type Config struct {
	Username string
	Since    time.Time
	Until    time.Time
	Repos    []NWO
}

type NWO struct {
	Owner string
	Name  string
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

// parseRepositories parses a comma-separated list of repositories in owner/name format
func parseRepositories(repoList string) ([]NWO, error) {
	if repoList == "" {
		return nil, fmt.Errorf("repository list cannot be empty")
	}

	var repos []NWO
	repoStrings := strings.Split(repoList, ",")

	for _, repoStr := range repoStrings {
		repoStr = strings.TrimSpace(repoStr)
		if repoStr == "" {
			continue // Skip empty entries
		}

		parts := strings.Split(repoStr, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid repository format '%s': expected 'owner/name'", repoStr)
		}

		owner := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])

		if owner == "" || name == "" {
			return nil, fmt.Errorf("invalid repository format '%s': owner and name cannot be empty", repoStr)
		}

		repos = append(repos, NWO{
			Owner: owner,
			Name:  name,
		})
	}

	if len(repos) == 0 {
		return nil, fmt.Errorf("no valid repositories found")
	}

	return repos, nil
}

func main() {
	// Parse command line arguments
	var (
		username    = flag.String("user", "", "GitHub username to filter PRs by assignee (required)")
		since       = flag.String("since", "", "Start date (YYYY-MM-DD format)")
		until       = flag.String("until", "", "End date (YYYY-MM-DD format)")
		days        = flag.Int("days", defaultDays, "Number of days back to search (used if since/until not specified)")
		outputDir   = flag.String("output-dir", "", "Output directory to write files (required)")
		extraPrompt = flag.String("extra-prompt", "", "File containing additional prompt text to append to the default prompt")
		repos       = flag.String("repos", "", "Comma-separated list of repositories in owner/name format (required)")
	)
	flag.Parse()

	// Validate required parameters
	if *username == "" {
		log.Fatalf("Username is required. Use -user flag to specify the GitHub username.")
	}
	if *outputDir == "" {
		log.Fatalf("Output directory is required. Use -output-dir flag to specify the directory.")
	}
	if *repos == "" {
		log.Fatalf("Repositories are required. Use -repos flag to specify repositories in owner/name format (e.g., 'github/token-scanning-service,owner/repo2').")
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory %s: %v", *outputDir, err)
	}

	// Parse repositories
	repoConfigs, parseErr := parseRepositories(*repos)
	if parseErr != nil {
		log.Fatalf("Failed to parse repositories: %v", parseErr)
	}

	// Parse dates
	var sinceTime, untilTime time.Time
	var err error

	if *since != "" && *until != "" {
		sinceTime, err = time.Parse(dateFormat, *since)
		if err != nil {
			log.Fatalf("Invalid since date format: %v", err)
		}
		untilTime, err = time.Parse(dateFormat, *until)
		if err != nil {
			log.Fatalf("Invalid until date format: %v", err)
		}
	} else {
		// Use days parameter
		untilTime = time.Now()
		sinceTime = untilTime.AddDate(0, 0, -*days)
	}

	config := Config{
		Username: *username,
		Since:    sinceTime,
		Until:    untilTime,
		Repos:    repoConfigs,
	}

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
	log.Printf("Counting PRs across %d repositories...", len(config.Repos))
	totalPRs := 0
	for _, repo := range config.Repos {
		count, err := countMergedPRs(ctx, client, repo, config)
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
	for _, repo := range config.Repos {
		prs, err := getMergedPRsWithProgress(ctx, client, repo, config, bar)
		if err != nil {
			log.Printf("Error fetching PRs from %s/%s: %v", repo.Owner, repo.Name, err)
			continue
		}
		allPRs = append(allPRs, prs...)
	}

	bar.Finish()
	log.Printf("Completed processing %d merged PRs", len(allPRs))

	// Write PR descriptions to the output directory
	prsFile := filepath.Join(*outputDir, "prs.md")
	log.Printf("Writing PR descriptions to %s", prsFile)
	if err := outputPRs(allPRs, prsFile); err != nil {
		log.Fatalf("Error writing PR descriptions to output file: %v", err)
	}

	// Use copilot CLI to summarize the content
	log.Printf("Generating summary with Copilot...")
	summary, err := generateSummaryWithCopilot(prsFile, *extraPrompt)
	if err != nil {
		log.Fatalf("Error generating summary: %v", err)
	}

	// Write summary to final output
	summaryFile := filepath.Join(*outputDir, "summary.md")
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
		config.Since.Format(dateFormat), config.Until.Format(dateFormat))

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

			// PR description - extract only the first section
			if strings.TrimSpace(pr.Description) != "" {
				fmt.Fprintf(writer, "#### Description\n\n")
				firstSection := extractFirstSection(pr.Description)
				fmt.Fprintf(writer, "%s\n\n", firstSection)
			} else {
				fmt.Fprintf(writer, "#### Description\n\n*No description provided.*\n\n")
			}

			// Separator between PRs
			fmt.Fprintf(writer, "---\n\n")
		}
	}

	return nil
}

// extractFirstSection extracts only the first section from a PR description
// that follows the standard template format
func extractFirstSection(description string) string {
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

// generateSummaryWithCopilot uses the copilot CLI to generate a summary of the PR descriptions
func generateSummaryWithCopilot(prsFilePath, extraPromptFile string) (string, error) {
	// Get the directory containing the prs file and the filename
	prsDir, err := filepath.Abs(filepath.Dir(prsFilePath))
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for directory: %w", err)
	}
	prsFileName := filepath.Base(prsFilePath)

	// Build the prompt starting with the default, using just the filename
	prompt := fmt.Sprintf(defaultPrompt, prsFileName)

	// Add custom instructions if provided
	if extraPromptFile != "" {
		// Read additional instructions from file
		customInstructions, err := os.ReadFile(extraPromptFile)
		if err != nil {
			return "", fmt.Errorf("failed to read instructions file %s: %w", extraPromptFile, err)
		}

		// Append additional instructions to the default prompt
		prompt = fmt.Sprintf("%s\n\nAdditional instructions:\n%s", prompt, strings.TrimSpace(string(customInstructions)))
	}

	log.Printf("Copilot prompt: %s", prompt)

	// Use copilot CLI with the directory added and reference the filename in the prompt
	cmd := exec.Command("copilot", "--add-dir", prsDir, "-p", prompt)
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
