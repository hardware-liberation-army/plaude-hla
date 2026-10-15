package tui

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/glamour/v2"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
var trailingStyledSpaces = regexp.MustCompile(`(\x1b\[[0-9;]*m ?)*$`)

func buildStyleJSON() []byte {
	return []byte(fmt.Sprintf(`{
  "document": {"block_prefix": "\n", "block_suffix": "\n", "color": "252", "margin": 2},
  "block_quote": {"indent": 1, "indent_token": "│ "},
  "paragraph": {},
  "list": {"level_indent": 2},
  "heading": {"block_suffix": "\n", "bold": true},
  "h1": {"prefix": " ", "suffix": " ", "color": "%s", "background_color": "63", "bold": true},
  "h2": {"color": "%s", "bold": true},
  "h3": {"color": "%s", "bold": true},
  "h4": {"color": "%s", "bold": true},
  "h5": {"color": "252", "bold": true},
  "h6": {"color": "246", "bold": false},
  "text": {},
  "strikethrough": {"crossed_out": true},
  "emph": {"italic": true},
  "strong": {"bold": true},
  "hr": {"color": "240", "format": "\n--------\n"},
  "item": {"block_prefix": "• "},
  "enumeration": {"block_prefix": ". "},
  "task": {"ticked": "[✓] ", "unticked": "[ ] "},
  "link": {"color": "30", "underline": true},
  "link_text": {"color": "35", "bold": true},
  "image": {"color": "212", "underline": true},
  "image_text": {"color": "243", "format": "Image: {{.text}} →"},
  "code": {"prefix": " ", "suffix": " ", "color": "203", "background_color": "236"},
  "code_block": {"color": "244", "margin": 2,
    "chroma": {
      "text": {"color": "#C4C4C4"},
      "error": {"color": "#F1F1F1", "background_color": "#F05B5B"},
      "comment": {"color": "#676767"},
      "comment_preproc": {"color": "#FF875F"},
      "keyword": {"color": "#00AAFF"},
      "keyword_reserved": {"color": "#FF5FD2"},
      "keyword_namespace": {"color": "#FF5F87"},
      "keyword_type": {"color": "#6E6ED8"},
      "operator": {"color": "#EF8080"},
      "punctuation": {"color": "#E8E8A8"},
      "name": {"color": "#C4C4C4"},
      "name_builtin": {"color": "#FF8EC7"},
      "name_tag": {"color": "#B083EA"},
      "name_attribute": {"color": "#7A7AE6"},
      "name_class": {"color": "#F1F1F1", "underline": true, "bold": true},
      "name_constant": {},
      "name_decorator": {"color": "#FFFF87"},
      "name_exception": {},
      "name_function": {"color": "#00D787"},
      "name_other": {},
      "literal": {},
      "literal_number": {"color": "#6EEFC0"},
      "literal_date": {},
      "literal_string": {"color": "#C69669"},
      "literal_string_escape": {"color": "#AFFFD7"},
      "generic_deleted": {"color": "#FD5B5B"},
      "generic_emph": {"italic": true},
      "generic_inserted": {"color": "#00D787"},
      "generic_strong": {"bold": true},
      "generic_subheading": {"color": "#777777"},
      "background": {"background_color": "#373737"}
    }
  },
  "table": {},
  "definition_list": {},
  "definition_term": {},
  "definition_description": {"block_prefix": "\n🠶 "},
  "html_block": {},
  "html_span": {}
}`,
		"178",
		"81",
		"76",
		"69",
	))
}

var cachedStyleJSON []byte

func getStyleJSON() []byte {
	if cachedStyleJSON == nil {
		cachedStyleJSON = buildStyleJSON()
	}
	return cachedStyleJSON
}

func renderMarkdown(content string, width int) string {
	if content == "" {
		return ""
	}

	w := width
	if w < 40 {
		w = 80
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes(getStyleJSON()),
		glamour.WithWordWrap(w-4),
	)
	if err != nil {
		return content
	}

	out, err := r.Render(content)
	if err != nil {
		return content
	}

	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = trailingStyledSpaces.ReplaceAllString(line, "")
		stripped := ansiRe.ReplaceAllString(line, "")
		if strings.TrimSpace(stripped) == "" {
			continue
		}
		lines = append(lines, "  "+line)
	}

	return strings.Join(lines, "\n")
}

func renderedMarkdownLines(content string, width int) []string {
	rendered := renderMarkdown(content, width)
	if rendered == "" {
		return nil
	}
	return strings.Split(rendered, "\n")
}
