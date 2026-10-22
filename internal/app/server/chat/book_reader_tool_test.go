package chat

import "testing"

func TestFindChapter(t *testing.T) {
	catalog := []bookChapter{
		{Book: "星辰之主", Chapter: 11, Path: "a.mp3"},
		{Book: "星辰之主", Chapter: 12, Path: "b.mp3"},
		{Book: "山海录", Chapter: 3, Path: "c.mp3"},
	}
	cases := []struct {
		name    string
		book    string
		chapter int
		want    string
		found   bool
	}{
		{"latest of a book", "星辰", 0, "b.mp3", true},
		{"specific chapter", "星辰之主", 11, "a.mp3", true},
		{"book name with extra words", "山海录这本书", 0, "c.mp3", true},
		{"no book picks newest entry", "", 0, "c.mp3", true},
		{"missing chapter", "山海录", 9, "", false},
		{"unknown book", "不存在", 0, "", false},
	}
	for _, tc := range cases {
		got, ok := findChapter(catalog, tc.book, tc.chapter)
		if ok != tc.found || (ok && got.Path != tc.want) {
			t.Errorf("%s: got %q,%v want %q,%v", tc.name, got.Path, ok, tc.want, tc.found)
		}
	}
}

func TestChapterMIMEType(t *testing.T) {
	for path, want := range map[string]string{"a.mp3": "audio/mpeg", "a.OGG": "audio/ogg", "a.wav": "audio/wav"} {
		if got := chapterMIMEType(path); got != want {
			t.Errorf("%s: got %s want %s", path, got, want)
		}
	}
}
