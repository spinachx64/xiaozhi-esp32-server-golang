package chat

import "testing"

func TestSanitizeTTSText(t *testing.T) {
	for in, want := range map[string]string{
		"哈囉～小智上線啦 😆 今天怎麼突然找我，": "哈囉～小智上線啦 今天怎麼突然找我，",
		"（笑）":     "",
		"好啊（笑）走吧": "好啊走吧",
		"我數了一下，總共 **12 個**工具喔 ✌️給你分類一下：": "我數了一下，總共 12 個工具喔 給你分類一下：",
		"- 🎵 音樂控制：":           "音樂控制：",
		"1 個- 🖥️ 螢幕、音量、裝置狀態：": "1 個 螢幕、音量、裝置狀態：",
		"## 标题":           "标题",
		"用 `go test` 跑一下": "用 go test 跑一下",
		"✌️😆":             "",
		"今天天气不错。":         "今天天气不错。",
		"1+1=2":           "1+1=2",
	} {
		if got := sanitizeTTSText(in); got != want {
			t.Errorf("sanitizeTTSText(%q) = %q, want %q", in, got, want)
		}
	}
}
