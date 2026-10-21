package delegate

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

const announceMaxRunes = 600

var reCodeFence = regexp.MustCompile("(?s)```.*?```")

// Announcement turns a finished task into text for TTS. Long answers are
// truncated; code blocks are not read aloud.
func Announcement(r Result) string {
	switch {
	case r.Err == nil:
		text := strings.TrimSpace(reCodeFence.ReplaceAllString(r.Text, "（代码已省略）"))
		return "你之前交代的任务完成了。" + truncateRunes(text, announceMaxRunes)
	case errors.Is(r.Err, context.DeadlineExceeded):
		return "抱歉，后台任务「" + truncateRunes(r.Task, 30) + "」超时了，没有完成。"
	default:
		hint := "后台服务暂时不可用。"
		var be *BridgeError
		if errors.As(r.Err, &be) && be.Message != "" {
			hint = "原因是：" + truncateRunes(be.Message, 80)
		}
		return "抱歉，后台任务「" + truncateRunes(r.Task, 30) + "」没有完成。" + hint
	}
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "……"
}
