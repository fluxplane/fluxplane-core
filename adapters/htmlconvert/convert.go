// Package htmlconvert provides HTML-to-Markdown conversion utilities.
package htmlconvert

import htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"

// ToMarkdown converts HTML to Markdown. If conversion fails, the original HTML
// is returned unchanged.
func ToMarkdown(html string) string {
	md, err := htmltomarkdown.ConvertString(html)
	if err != nil {
		return html
	}
	return md
}
