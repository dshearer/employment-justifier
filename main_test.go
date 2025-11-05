package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractFirstSection(t *testing.T) {
	tests := []struct {
		name        string
		description string
		expected    string
	}{
		{
			name: "Standard template format with first section",
			description: `### What are you trying to accomplish?

This PR adds a new feature for token validation that improves security.
It implements proper error handling and adds comprehensive logging.

### How is it being implemented?

The implementation uses a new service class...

### How can the changes be tested?

Run the test suite...`,
			expected: `This PR adds a new feature for token validation that improves security.
It implements proper error handling and adds comprehensive logging.`,
		},
		{
			name: "Standard template with empty first section",
			description: `### What are you trying to accomplish?

### How is it being implemented?

The implementation uses a new service class...`,
			expected: `### What are you trying to accomplish?

### How is it being implemented?

The implementation uses a new service class...`,
		},
		{
			name: "Standard template with whitespace in first section",
			description: `### What are you trying to accomplish?


   This PR fixes a critical bug.


### How is it being implemented?

Using a different approach...`,
			expected: `This PR fixes a critical bug.`,
		},
		{
			name: "Standard template with multiple sections",
			description: `### What are you trying to accomplish?

Fix authentication issues in the token scanning service.
This addresses security vulnerabilities found during audit.

### How is it being implemented?

- Updated authentication logic
- Added new validation checks
- Improved error messages

### How can the changes be tested?

1. Run integration tests
2. Verify with test tokens
3. Check error handling

### Additional Notes

This change is backwards compatible.`,
			expected: `Fix authentication issues in the token scanning service.
This addresses security vulnerabilities found during audit.`,
		},
		{
			name: "Non-standard format - returns original",
			description: `This is a simple PR description without the standard template format.
It just describes what was changed in a free-form manner.

Some additional details here.`,
			expected: `This is a simple PR description without the standard template format.
It just describes what was changed in a free-form manner.

Some additional details here.`,
		},
		{
			name:        "Empty description",
			description: ``,
			expected:    ``,
		},
		{
			name: "Only whitespace",
			description: `

   `,
			expected: `

   `,
		},
		{
			name: "Standard template but wrong section header",
			description: `### What is this PR about?

This is not the standard format.

### Implementation details

Details here...`,
			expected: `### What is this PR about?

This is not the standard format.

### Implementation details

Details here...`,
		},
		{
			name: "Standard section header at end",
			description: `Some description here.

### What are you trying to accomplish?

This appears later in the description.`,
			expected: `This appears later in the description.`,
		},
		{
			name: "Multiple standard sections mixed",
			description: `### What are you trying to accomplish?

First section content here.
Multiple lines of content.

### Some other section

Other content.

### What are you trying to accomplish?

This shouldn't be processed as we already found the first one.`,
			expected: `First section content here.
Multiple lines of content.`,
		},
		{
			name: "Section header with different casing",
			description: `### what are you trying to accomplish?

This should not match due to case sensitivity.`,
			expected: `### what are you trying to accomplish?

This should not match due to case sensitivity.`,
		},
		{
			name: "Section header with extra spaces",
			description: `###  What are you trying to accomplish?

Content with extra spaces in header.

### Next section

More content.`,
			expected: `###  What are you trying to accomplish?

Content with extra spaces in header.

### Next section

More content.`,
		},
		{
			name:        "Only the section header, no content",
			description: `### What are you trying to accomplish?`,
			expected:    `### What are you trying to accomplish?`,
		},
		{
			name: "Section header followed by immediate next section",
			description: `### What are you trying to accomplish?
### How is it being implemented?

Implementation details...`,
			expected: `### What are you trying to accomplish?
### How is it being implemented?

Implementation details...`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFirstSection(tt.description)
			assert.Equal(t, tt.expected, result, "extractFirstSection should return expected output")
		})
	}
}

func TestExtractFirstSection_EdgeCases(t *testing.T) {
	t.Run("Very long first section", func(t *testing.T) {
		longContent := "This is a very long section content. "
		for i := 0; i < 100; i++ {
			longContent += "Line " + string(rune(i+'0')) + " of the first section. "
		}

		description := `### What are you trying to accomplish?

` + longContent + `

### How is it being implemented?

Implementation details...`

		result := extractFirstSection(description)
		assert.Contains(t, result, "This is a very long section content")
		assert.Contains(t, result, "Line")
		assert.NotContains(t, result, "Implementation details")
	})

	t.Run("Section with code blocks", func(t *testing.T) {
		description := `### What are you trying to accomplish?

This PR adds a new function:

` + "```go" + `
func newFunction() {
    return "hello"
}
` + "```" + `

It improves performance significantly.

### How is it being implemented?

Using goroutines...`

		expected := `This PR adds a new function:

` + "```go" + `
func newFunction() {
    return "hello"
}
` + "```" + `

It improves performance significantly.`

		result := extractFirstSection(description)
		assert.Equal(t, expected, result)
	})

	t.Run("Section with nested headers", func(t *testing.T) {
		description := `### What are you trying to accomplish?

This PR includes:

#### Subheading 1
Some details here.

#### Subheading 2
More details here.

### How is it being implemented?

Implementation...`

		// Note: #### headers also match the "###" prefix, so they will break the extraction
		expected := `This PR includes:`

		result := extractFirstSection(description)
		assert.Equal(t, expected, result)
	})
}
