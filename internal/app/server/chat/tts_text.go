package chat

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/spf13/viper"
)

var (
	// Short stage directions such as "（笑）" or "(叹气)": shown as subtitles,
	// not read aloud.
	reTTSStageDirection = regexp.MustCompile(`（\p{Han}{1,4}）|\(\p{Han}{1,4}\)`)
	// Markdown list markers at the start of a line or right after a CJK
	// character / punctuation (streamed segments lose their line breaks).
	reTTSListMarker = regexp.MustCompile(`(^|[\n\p{Han}：:，。！？])\s*(?:[-•]|\d+[.、])\s+`)
	reTTSHeading    = regexp.MustCompile(`(?m)^\s*#{1,6}\s*`)
	reTTSSpaces     = regexp.MustCompile(`[ \t]{2,}`)
)

// sanitizeTTSText strips Markdown, emoji and stage directions before text is
// sent to the TTS provider. Some providers stall on such input, and none of
// it is meant to be spoken. The result may be empty: skip the sentence then.
func sanitizeTTSText(text string) string {
	text = reTTSStageDirection.ReplaceAllString(text, "")
	text = reTTSHeading.ReplaceAllString(text, "")
	text = reTTSListMarker.ReplaceAllString(text, "$1")

	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '*' || r == '`' || r == '#' || r == '|':
		case r == '‍' || (r >= '︀' && r <= '️'): // ZWJ, variation selectors
		case unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) || unicode.Is(unicode.Cs, r): // emoji and symbols
		case r >= 0x1F000: // remaining emoji planes
		default:
			b.WriteRune(r)
		}
	}
	text = reTTSSpaces.ReplaceAllString(b.String(), " ")
	text = strings.TrimSpace(text)
	if !hasSpeakableRune(text) {
		return ""
	}
	return text
}

func hasSpeakableRune(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// defaultSpokenOutputRule is appended to the system prompt. The answer is
// spoken by TTS, so formatting only gets in the way.
const defaultSpokenOutputRule = "你的回答会被直接转成语音播放：用口语化的短句回答，不要使用 Markdown、标题、列表、加粗、表格、代码块、emoji，也不要写括号里的动作或表情描写。"

// spokenOutputRule reads chat.spoken_output_rule; an empty string disables it.
func spokenOutputRule() string {
	if viper.IsSet("chat.spoken_output_rule") {
		return strings.TrimSpace(viper.GetString("chat.spoken_output_rule"))
	}
	return defaultSpokenOutputRule
}
