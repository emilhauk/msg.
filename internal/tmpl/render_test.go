package tmpl

import (
	"html/template"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderInline(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  template.HTML
	}{
		{
			name:  "plain text is escaped",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "HTML special chars are escaped",
			input: "<script>alert(1)</script>",
			want:  "&lt;script&gt;alert(1)&lt;/script&gt;",
		},
		{
			name:  "bold with double asterisks",
			input: "**bold**",
			want:  "<strong>bold</strong>",
		},
		{
			name:  "bold with spaces inside",
			input: "**hello world**",
			want:  "<strong>hello world</strong>",
		},
		{
			name:  "italic with single asterisk",
			input: "*italic*",
			want:  "<em>italic</em>",
		},
		{
			name:  "italic with spaces inside",
			input: "*hello world*",
			want:  "<em>hello world</em>",
		},
		{
			name:  "single char italic",
			input: "*a*",
			want:  "<em>a</em>",
		},
		{
			name:  "bold and italic in same line",
			input: "**bold** and *italic*",
			want:  "<strong>bold</strong> and <em>italic</em>",
		},
		{
			name:  "italic does not match space-padded asterisks",
			input: "* not italic *",
			want:  "* not italic *",
		},
		{
			name:  "italic does not match trailing space before closing asterisk",
			input: "*trailing space *",
			want:  "*trailing space *",
		},
		{
			name:  "italic does not match leading space after opening asterisk",
			input: "* leading space*",
			want:  "* leading space*",
		},
		{
			name:  "unclosed bold is not formatted",
			input: "**unclosed",
			want:  "**unclosed",
		},
		{
			name:  "unclosed italic is not formatted",
			input: "*unclosed",
			want:  "*unclosed",
		},
		{
			name:  "bold wraps escaped HTML",
			input: "**<b>not html</b>**",
			want:  "<strong>&lt;b&gt;not html&lt;/b&gt;</strong>",
		},
		{
			name:  "italic wraps escaped HTML",
			input: "*<em>not html</em>*",
			want:  "<em>&lt;em&gt;not html&lt;/em&gt;</em>",
		},
		{
			name:  "URL is linkified",
			input: "visit https://example.com now",
			want:  `visit <a href="https://example.com" target="_blank" rel="noopener noreferrer">https://example.com</a> now`,
		},
		{
			name:  "multiple bold spans",
			input: "**a** and **b**",
			want:  "<strong>a</strong> and <strong>b</strong>",
		},
		{
			name:  "bold takes priority over italic for double asterisks",
			input: "**bold**",
			want:  "<strong>bold</strong>",
		},
		{
			name:  "text around formatting is escaped",
			input: "a & b **bold** c & d",
			want:  "a &amp; b <strong>bold</strong> c &amp; d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderInline(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRenderMarkdownBlock(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  template.HTML
	}{
		{
			name:  "plain text unchanged",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "multiline plain text preserves newlines",
			input: "line one\nline two",
			want:  "line one\nline two",
		},
		{
			name:  "unordered list with dash",
			input: "- item one",
			want:  "<ul><li>item one</li></ul>",
		},
		{
			name:  "unordered list with asterisk",
			input: "* item one",
			want:  "<ul><li>item one</li></ul>",
		},
		{
			name:  "multiple unordered list items",
			input: "- first\n- second\n- third",
			want:  "<ul><li>first</li><li>second</li><li>third</li></ul>",
		},
		{
			name:  "ordered list",
			input: "1. first\n2. second",
			want:  "<ol><li>first</li><li>second</li></ol>",
		},
		{
			name:  "ordered list with non-sequential numbers",
			input: "1. first\n3. third",
			want:  "<ol><li>first</li><li>third</li></ol>",
		},
		{
			// No \n before <ul>: it is a block element, browser breaks the line.
			name:  "text before unordered list",
			input: "intro:\n- item",
			want:  "intro:<ul><li>item</li></ul>",
		},
		{
			name:  "text after unordered list",
			input: "- item\noutro",
			want:  "<ul><li>item</li></ul>\noutro",
		},
		{
			// The \n between "before" and "- item" is consumed by splitting into
			// lines; the list block element handles visual separation.
			name:  "text around list",
			input: "before\n- item\nafter",
			want:  "before<ul><li>item</li></ul>\nafter",
		},
		{
			name:  "list items with inline bold",
			input: "- **bold item**",
			want:  "<ul><li><strong>bold item</strong></li></ul>",
		},
		{
			name:  "list items with inline italic",
			input: "- *italic item*",
			want:  "<ul><li><em>italic item</em></li></ul>",
		},
		{
			name:  "inline bold in plain text",
			input: "say **hello** there",
			want:  "say <strong>hello</strong> there",
		},
		{
			name:  "HTML is escaped in plain text",
			input: "<script>",
			want:  "&lt;script&gt;",
		},
		{
			// Both are block elements; no \n separator needed between them.
			name:  "mixed unordered then ordered list",
			input: "- a\n1. b",
			want:  "<ul><li>a</li></ul><ol><li>b</li></ol>",
		},
		{
			name:  "single-line blockquote",
			input: "> hello world",
			want:  `<blockquote class="message__blockquote">hello world</blockquote>`,
		},
		{
			name:  "multi-line blockquote",
			input: "> line one\n> line two",
			want:  `<blockquote class="message__blockquote">line one` + "\n" + `line two</blockquote>`,
		},
		{
			name:  "blockquote with inline bold",
			input: "> **bold quote**",
			want:  `<blockquote class="message__blockquote"><strong>bold quote</strong></blockquote>`,
		},
		{
			name:  "blockquote with inline italic and link",
			input: "> *italic* and https://example.com",
			want:  `<blockquote class="message__blockquote"><em>italic</em> and <a href="https://example.com" target="_blank" rel="noopener noreferrer">https://example.com</a></blockquote>`,
		},
		{
			name:  "blockquote followed by regular text",
			input: "> quoted\nregular",
			want:  `<blockquote class="message__blockquote">quoted</blockquote>` + "\n" + `regular`,
		},
		{
			name:  "blockquote with bare > line",
			input: ">bare",
			want:  `<blockquote class="message__blockquote">bare</blockquote>`,
		},
		{
			name:  "list inside blockquote",
			input: "> - first\n> - second",
			want:  `<blockquote class="message__blockquote"><ul><li>first</li><li>second</li></ul></blockquote>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderMarkdownBlock(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRenderText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  template.HTML
	}{
		{
			name:  "code block is highlighted and wrapped",
			input: "```go\nfmt.Println()\n```",
			want:  `<div class="code-block">`,
		},
		{
			name:  "text before code block is markdown-processed",
			input: "**bold** before\n```go\ncode\n```",
			want:  `<strong>bold</strong> before`,
		},
		{
			name:  "text after code block is markdown-processed",
			input: "```go\ncode\n```\n- list item",
			want:  `<ul><li>list item</li></ul>`,
		},
		{
			name:  "list and bold together",
			input: "- **bold**\n- *italic*",
			want:  `<ul><li><strong>bold</strong></li><li><em>italic</em></li></ul>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderText(tt.input)
			assert.Contains(t, string(got), string(tt.want))
		})
	}
}
