package tmpl

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/rs/zerolog/log"

	"github.com/emilhauk/msg/internal/model"
)

// urlPattern matches http and https URLs in message text.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

// linkify replaces URLs in s with clickable <a> tags. Non-URL text is
// HTML-escaped normally so the returned template.HTML is safe to use with
// html/template's {{...}} without further escaping.
func linkify(s string) template.HTML {
	var b strings.Builder
	last := 0
	for _, loc := range urlPattern.FindAllStringIndex(s, -1) {
		start, end := loc[0], loc[1]
		// Escape and write the plain-text segment before this URL.
		b.WriteString(template.HTMLEscapeString(s[last:start]))
		raw := s[start:end]
		// Validate the URL before turning it into a link.
		if u, err := url.ParseRequestURI(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			b.WriteString(`<a href="`)
			b.WriteString(template.HTMLEscapeString(raw))
			b.WriteString(`" target="_blank" rel="noopener noreferrer">`)
			b.WriteString(template.HTMLEscapeString(raw))
			b.WriteString(`</a>`)
		} else {
			b.WriteString(template.HTMLEscapeString(raw))
		}
		last = end
	}
	b.WriteString(template.HTMLEscapeString(s[last:]))
	return template.HTML(b.String())
}

// inlineRe matches inline markdown patterns and URLs in priority order.
// Inline code (`text`) comes first so backtick-wrapped content is not parsed for other markdown.
// Bold (**text**) must come before italic (*text*) to avoid ** consuming just one *.
// Groups: (1) `code`, (2) **bold**, (3) URL, (4) *italic*, (5) ~~strikethrough~~, (6) ~strikethrough~
// Double tilde must come before single to avoid ~~ consuming just one ~.
var inlineRe = regexp.MustCompile(
	"`([^`]+)`" +
		`|\*\*(.+?)\*\*` +
		`|(https?://[^\s<>"]+)` +
		`|\*(\S(?:.+?\S)?)\*` +
		`|~~(.+?)~~` +
		`|~(.+?)~`,
)

// renderInline applies inline code (`text`), bold (**text**), italic (*text*),
// strikethrough (~~text~~), and URL linkification to s, HTML-escaping all
// plain-text segments. The result is safe HTML.
func renderInline(s string) template.HTML {
	var b strings.Builder
	last := 0
	for _, loc := range inlineRe.FindAllStringSubmatchIndex(s, -1) {
		matchStart, matchEnd := loc[0], loc[1]
		b.WriteString(template.HTMLEscapeString(s[last:matchStart]))
		switch {
		case loc[2] >= 0: // `code`
			b.WriteString("<code>")
			b.WriteString(template.HTMLEscapeString(s[loc[2]:loc[3]]))
			b.WriteString("</code>")
		case loc[4] >= 0: // **bold**
			b.WriteString("<strong>")
			b.WriteString(template.HTMLEscapeString(s[loc[4]:loc[5]]))
			b.WriteString("</strong>")
		case loc[6] >= 0: // URL
			raw := s[loc[6]:loc[7]]
			if u, err := url.ParseRequestURI(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
				b.WriteString(`<a href="`)
				b.WriteString(template.HTMLEscapeString(raw))
				b.WriteString(`" target="_blank" rel="noopener noreferrer">`)
				b.WriteString(template.HTMLEscapeString(raw))
				b.WriteString(`</a>`)
			} else {
				b.WriteString(template.HTMLEscapeString(raw))
			}
		case loc[8] >= 0: // *italic*
			b.WriteString("<em>")
			b.WriteString(template.HTMLEscapeString(s[loc[8]:loc[9]]))
			b.WriteString("</em>")
		case loc[10] >= 0: // ~~strikethrough~~
			b.WriteString("<del>")
			b.WriteString(template.HTMLEscapeString(s[loc[10]:loc[11]]))
			b.WriteString("</del>")
		case loc[12] >= 0: // ~strikethrough~
			b.WriteString("<del>")
			b.WriteString(template.HTMLEscapeString(s[loc[12]:loc[13]]))
			b.WriteString("</del>")
		}
		last = matchEnd
	}
	b.WriteString(template.HTMLEscapeString(s[last:]))
	return template.HTML(b.String())
}

// blockquoteRe matches lines that are blockquote items (> followed by optional space).
var blockquoteRe = regexp.MustCompile(`^> ?`)

// ulItemRe matches lines that are unordered list items (- or * followed by space).
var ulItemRe = regexp.MustCompile(`^[*-] `)

// olItemRe matches lines that are ordered list items (digits followed by ". ").
var olItemRe = regexp.MustCompile(`^\d+\. `)

// renderMarkdownBlock processes a plain-text segment applying block-level (lists)
// and inline (bold, italic, links) markdown formatting.
func renderMarkdownBlock(s string) template.HTML {
	lines := strings.Split(s, "\n")
	var out strings.Builder
	i := 0
	for i < len(lines) {
		line := lines[i]
		if blockquoteRe.MatchString(line) {
			var inner []string
			for i < len(lines) && blockquoteRe.MatchString(lines[i]) {
				inner = append(inner, blockquoteRe.ReplaceAllString(lines[i], ""))
				i++
			}
			out.WriteString(`<blockquote class="message__blockquote">`)
			out.WriteString(string(renderMarkdownBlock(strings.Join(inner, "\n"))))
			out.WriteString(`</blockquote>`)
			continue
		}
		if ulItemRe.MatchString(line) {
			out.WriteString("<ul>")
			for i < len(lines) && ulItemRe.MatchString(lines[i]) {
				out.WriteString("<li>")
				out.WriteString(string(renderInline(lines[i][2:])))
				out.WriteString("</li>")
				i++
			}
			out.WriteString("</ul>")
			continue
		}
		if olItemRe.MatchString(line) {
			out.WriteString("<ol>")
			for i < len(lines) && olItemRe.MatchString(lines[i]) {
				m := olItemRe.FindStringIndex(lines[i])
				out.WriteString("<li>")
				out.WriteString(string(renderInline(lines[i][m[1]:])))
				out.WriteString("</li>")
				i++
			}
			out.WriteString("</ol>")
			continue
		}
		// Plain line: emit a newline separator before all but the first item.
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(string(renderInline(line)))
		i++
	}
	return template.HTML(out.String())
}

// codeFenceRe matches fenced code blocks: ```lang\ncode\n``` (lang is optional).
var codeFenceRe = regexp.MustCompile("(?s)```([a-zA-Z0-9+#-]*)\\n(.*?)```")

// chromaFormatter is shared and CSS-class-based (styles come from style.css).
var chromaFormatter = chromahtml.New(
	chromahtml.WithClasses(true),
	chromahtml.WithLineNumbers(false),
	chromahtml.PreventSurroundingPre(true), // we emit our own <pre>
)

// ChromaCSS returns the CSS for the named chroma style (used once at startup
// to embed the stylesheet). Falls back to "github" if the name is unknown.
func ChromaCSS(styleName string) (string, error) {
	s := styles.Get(styleName)
	if s == nil {
		s = styles.Fallback
	}
	var buf bytes.Buffer
	if err := chromaFormatter.WriteCSS(&buf, s); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// highlightCode returns syntax-highlighted HTML for a code snippet. lang may
// be empty, in which case chroma will try to detect the language.
func highlightCode(lang, code string) template.HTML {
	var lex = lexers.Get(lang)
	if lex == nil {
		lex = lexers.Analyse(code)
	}
	if lex == nil {
		lex = lexers.Fallback
	}

	iterator, err := lex.Tokenise(nil, code)
	if err != nil {
		// Safe fallback: just escape the code.
		return template.HTML("<code>" + template.HTMLEscapeString(code) + "</code>")
	}

	var buf bytes.Buffer
	if err := chromaFormatter.Format(&buf, styles.Fallback, iterator); err != nil {
		return template.HTML("<code>" + template.HTMLEscapeString(code) + "</code>")
	}
	return template.HTML(buf.String())
}

// renderText transforms raw message text into safe HTML. It handles:
//   - Fenced code blocks (```lang\ncode\n```) → syntax-highlighted <pre> with copy button
//   - Plain text segments → markdown-formatted (bold, italic, lists) and linkified
func renderText(s string) template.HTML {
	var out strings.Builder

	last := 0
	for _, loc := range codeFenceRe.FindAllStringSubmatchIndex(s, -1) {
		// loc[0]:loc[1] = full match
		// loc[2]:loc[3] = lang capture
		// loc[4]:loc[5] = code capture
		matchStart, matchEnd := loc[0], loc[1]
		lang := s[loc[2]:loc[3]]
		code := s[loc[4]:loc[5]]

		// Emit markdown-formatted text before this code block.
		out.WriteString(string(renderMarkdownBlock(s[last:matchStart])))

		// Highlighted code.
		highlighted := highlightCode(lang, code)

		// Wrap in a code-block widget with a copy button.
		langLabel := lang
		if langLabel == "" {
			langLabel = "code"
		}
		out.WriteString(`<div class="code-block">`)
		out.WriteString(`<div class="code-block__header">`)
		out.WriteString(`<span class="code-block__lang">` + template.HTMLEscapeString(langLabel) + `</span>`)
		out.WriteString(`<button class="code-block__copy btn--icon" aria-label="Copy code" data-copy-code>`)
		out.WriteString(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`)
		out.WriteString(`</button>`)
		out.WriteString(`</div>`)
		out.WriteString(`<pre class="code-block__pre chroma"><code>`)
		out.WriteString(string(highlighted))
		out.WriteString(`</code></pre>`)
		// Hidden element holds raw text for the copy action.
		out.WriteString(`<textarea class="code-block__raw" aria-hidden="true" tabindex="-1" readonly>` + template.HTMLEscapeString(code) + `</textarea>`)
		out.WriteString(`</div>`)

		last = matchEnd
	}

	// Remaining text.
	out.WriteString(string(renderMarkdownBlock(s[last:])))

	return template.HTML(out.String())
}

// ReactionsTemplateData is the shape passed to the reactions.html template.
type ReactionsTemplateData struct {
	MsgID     string
	RoomID    string
	Reactions []model.Reaction
}

// reactionData builds the ReactionsTemplateData from a message's fields.
// It is exposed as a template function so message.html can call it inline.
func reactionData(msgID, roomID string, reactions []model.Reaction) ReactionsTemplateData {
	return ReactionsTemplateData{
		MsgID:     msgID,
		RoomID:    roomID,
		Reactions: reactions,
	}
}

// isImageType reports whether a MIME type is a supported image type.
func isImageType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

// isVideoType reports whether a MIME type is a supported video type.
func isVideoType(contentType string) bool {
	switch contentType {
	case "video/mp4", "video/webm":
		return true
	}
	return false
}

var funcMap = template.FuncMap{
	"linkify":      linkify,
	"renderText":   renderText,
	"reactionData": reactionData,
	"isImageType":  isImageType,
	"isVideoType":  isVideoType,
}

// Renderer holds parsed templates.
type Renderer struct {
	templates    map[string]*template.Template
	BuildVersion string
}

// pageData wraps any template data to inject global fields (e.g. BuildVersion).
type pageData struct {
	BuildVersion string
	Data         any
}

// New parses all templates from the given filesystem.
// Partials (message.html, unfurl.html, reactions.html) are also registered as
// standalone templates for use in SSE responses.
func New(fsys fs.FS) (*Renderer, error) {
	base := "templates/base.html"
	msgPartial := "templates/message.html"
	reactionsPartial := "templates/reactions.html"

	pages := []string{
		"templates/room.html",
		"templates/login.html",
		"templates/error.html",
		"templates/no-rooms.html",
	}

	r := &Renderer{templates: make(map[string]*template.Template)}

	// Full-page templates: base + page + message partial + reactions partial.
	for _, page := range pages {
		name := templateName(page)
		files := []string{base, page}
		if page != msgPartial {
			files = append(files, msgPartial, reactionsPartial)
		}
		t, err := template.New(name).Funcs(funcMap).ParseFS(fsys, files...)
		if err != nil {
			return nil, fmt.Errorf("tmpl: parse %s: %w", page, err)
		}
		r.templates[name] = t
	}

	// message.html standalone (returned by SSE message events).
	// Needs reactions.html because it calls {{template "reactions.html" ...}}.
	{
		name := "message.html"
		t, err := template.New(name).Funcs(funcMap).ParseFS(fsys, msgPartial, reactionsPartial)
		if err != nil {
			return nil, fmt.Errorf("tmpl: parse partial message.html: %w", err)
		}
		r.templates[name] = t
	}

	// reactions.html standalone (published via SSE reaction events).
	{
		name := "reactions.html"
		t, err := template.New(name).Funcs(funcMap).ParseFS(fsys, reactionsPartial)
		if err != nil {
			return nil, fmt.Errorf("tmpl: parse partial reactions.html: %w", err)
		}
		r.templates[name] = t
	}

	// unfurl.html standalone (published via SSE unfurl events).
	{
		name := "unfurl.html"
		t, err := template.New(name).Funcs(funcMap).ParseFS(fsys, "templates/unfurl.html")
		if err != nil {
			return nil, fmt.Errorf("tmpl: parse partial unfurl.html: %w", err)
		}
		r.templates[name] = t
	}

	// history.html includes message.html (and transitively reactions.html).
	{
		name := "history.html"
		t, err := template.New(name).Funcs(funcMap).ParseFS(fsys, "templates/history.html", msgPartial, reactionsPartial)
		if err != nil {
			return nil, fmt.Errorf("tmpl: parse partial history.html: %w", err)
		}
		r.templates[name] = t
	}

	// room-panel.html — room settings panel, loaded lazily via HTMX.
	{
		name := "room-panel.html"
		t, err := template.New(name).Funcs(funcMap).ParseFS(fsys, "templates/room-panel.html")
		if err != nil {
			return nil, fmt.Errorf("tmpl: parse partial room-panel.html: %w", err)
		}
		r.templates[name] = t
	}

	return r, nil
}

// Render executes the named template and writes the result to w.
func (r *Renderer) Render(w http.ResponseWriter, status int, name string, data any) {
	t, ok := r.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	wrapped := pageData{BuildVersion: r.BuildVersion, Data: data}
	if err := t.ExecuteTemplate(w, name, wrapped); err != nil {
		// Headers already sent; log the error.
		log.Error().Err(err).Str("template", name).Msg("tmpl: execute")
	}
}

// ErrorData is the data passed to the error.html template.
type ErrorData struct {
	User       any // *model.User or nil; typed as any to avoid import cycle
	StatusCode int
	Title      string
	Message    string
}

// RenderError renders the shared error page with the given HTTP status code.
func (r *Renderer) RenderError(w http.ResponseWriter, status int, data ErrorData) {
	data.StatusCode = status
	r.Render(w, status, "error.html", data)
}

// RenderPartial renders a partial template (no base layout, data passed
// unwrapped) directly to w. Used for HTMX-loaded fragments.
func (r *Renderer) RenderPartial(w http.ResponseWriter, status int, name string, data any) {
	t, ok := r.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		log.Error().Err(err).Str("template", name).Msg("tmpl: execute partial")
	}
}

// RenderString executes the named template and returns the result as a string.
// Partials rendered via RenderString (SSE fragments) do not use the base layout
// so they do not need BuildVersion — data is passed through unwrapped.
// Leading and trailing whitespace is trimmed so that callers can safely use
// the result as a payload segment without worrying about leading newlines from
// the {{define}} block boundary.
func (r *Renderer) RenderString(name string, data any) (string, error) {
	t, ok := r.templates[name]
	if !ok {
		return "", fmt.Errorf("tmpl: template not found: %s", name)
	}
	var buf []byte
	w := &bytesWriter{buf: &buf}
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf)), nil
}

type bytesWriter struct{ buf *[]byte }

func (b *bytesWriter) Write(p []byte) (int, error) {
	*b.buf = append(*b.buf, p...)
	return len(p), nil
}

// templateName extracts the base filename without the directory prefix.
func templateName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
