package chat

import "testing"

func TestIsFillerOnlyASRText(t *testing.T) {
	for text, want := range map[string]bool{
		"嗯":       true,
		"嗯。":      true,
		"嗯嗯？":     true,
		" 啊， 呃… ": true,
		"哦!":      true,
		"":        false,
		"。":       false,
		"嗯好":      false,
		"嗯，放首歌":   false,
		"好的":      false,
		"ok":      false,
	} {
		if got := isFillerOnlyASRText(text); got != want {
			t.Errorf("isFillerOnlyASRText(%q) = %v, want %v", text, got, want)
		}
	}
}
