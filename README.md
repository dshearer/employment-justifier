# Employment Justifier

A tool to analyze GitHub pull requests and generate performance review summaries.

## Prerequisites

Before using this tool, you need to have the following CLI tools installed and configured:

### Required Tools

1. **GitHub CLI (`gh`)**
   - Install: Follow the instructions at [cli.github.com](https://cli.github.com/)
   - Login: Run `gh auth login` to authenticate with GitHub

2. **GitHub Copilot CLI (`copilot`)**
   - Install: Follow the instructions at [docs.github.com/en/copilot/github-copilot-in-the-cli](https://docs.github.com/en/copilot/github-copilot-in-the-cli)
   - Requires a GitHub Copilot subscription

### Authentication

Make sure you're logged into GitHub with the `gh` CLI:

```bash
gh auth login
```

You can verify your authentication status with:

```bash
gh auth status
```

## Usage

```bash
go run . -user <github-username> -repos <owner/repo1,owner/repo2> -output-dir <directory> [options]
```

### Required Parameters

- `-user`: GitHub username to filter PRs by assignee
- `-repos`: Comma-separated list of repositories in owner/name format
- `-output-dir`: Directory where output files will be written

### Optional Parameters

- `-since`: Start date (YYYY-MM-DD format)
- `-until`: End date (YYYY-MM-DD format)
- `-days`: Number of days back to search (default: 30, used if since/until not specified)
- `-extra-prompt`: File containing additional prompt text for the summary generation

### Example

```bash
go run . -user johndoe -repos "github/cli,microsoft/vscode" -output-dir ./results -days 90
```

## Output

The tool generates two files in the specified output directory:

- `prs.md`: Detailed information about all merged pull requests
- `summary.md`: AI-generated summary of contributions and impact
