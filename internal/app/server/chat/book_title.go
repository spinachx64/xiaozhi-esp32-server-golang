package chat

import "strings"

// splitBookTitle splits a chapter title of the form "《书名》章节名" into the
// book and chapter parts. It reports false for other titles (e.g. songs).
func splitBookTitle(title string) (book, chapter string, ok bool) {
	title = strings.TrimSpace(title)
	rest, found := strings.CutPrefix(title, "《")
	if !found {
		return "", "", false
	}
	book, chapter, found = strings.Cut(rest, "》")
	book = strings.TrimSpace(book)
	chapter = strings.TrimSpace(chapter)
	if !found || book == "" {
		return "", "", false
	}
	return book, chapter, true
}
