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

The tool now uses a configuration file instead of command-line arguments for better maintainability.

### Quick Start

1. **Create a configuration file** (`config.yaml`) with your settings:
   ```yaml
   username: your-github-username
   since: "2025-05-01"
   until: "2025-10-31"
   output_dir: "./output"
   repos:
     - "github/token-scanning-service"
     - "owner/another-repo"
   ```

2. **Run the tool:**
   ```bash
   go run . -config config.yaml
   ```

### Configuration File Format

The configuration file uses YAML format with the following fields:

#### Required Fields
- `username`: GitHub username to filter PRs by
- `output_dir`: Directory where output files will be written
- `repos`: List of repositories in "owner/name" format

#### Optional Fields
- `since`: Start date (YYYY-MM-DD format)
- `until`: End date (YYYY-MM-DD format)
- `days`: Number of days back to search (default: 30, used if since/until not specified)
- `extra_prompt`: Path to file containing additional prompt instructions for Copilot

### Command Line Options

- `-config`: Path to configuration file (default: `config.yaml`)

### Example Configuration

```yaml
username: johndoe
days: 90
output_dir: ./results
repos:
  - "github/cli"
  - "microsoft/vscode"
extra_prompt: "custom-instructions.txt"
```

## Output

The tool generates two files in the specified output directory:

- `prs.md`: Detailed information about all merged pull requests
- `summary.md`: AI-generated summary of contributions and impact
