package chat

import (
	"strings"
	"unicode"

	"github.com/spf13/viper"
)

// asrFillerRunes are interjections the ASR produces for key clicks, breathing
// or background noise. A result made only of these (and punctuation) is not
// something the user said to the assistant.
var asrFillerRunes = map[rune]bool{
	'嗯': true, '恩': true, '啊': true, '呃': true, '额': true, '哦': true,
	'喔': true, '噢': true, '唔': true, '呣': true, '哼': true, '嘿': true,
	'哈': true, '呀': true, '诶': true, '欸': true, '唉': true, '哎': true,
}

// asrFillerFilterEnabled reads chat.asr_filler_filter (default true).
func asrFillerFilterEnabled() bool {
	if !viper.IsSet("chat.asr_filler_filter") {
		return true
	}
	return viper.GetBool("chat.asr_filler_filter")
}

// isFillerOnlyASRText reports whether text contains nothing but filler
// interjections, punctuation and spaces, e.g. "嗯。", "啊啊", "嗯嗯？".
func isFillerOnlyASRText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	hasFiller := false
	for _, r := range text {
		switch {
		case asrFillerRunes[r]:
			hasFiller = true
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r):
		default:
			return false
		}
	}
	return hasFiller
}
