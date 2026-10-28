package chat

import (
	"testing"

	mcp_go "github.com/mark3labs/mcp-go/mcp"
)

func TestMCPAudioDurationMetadata(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int64
	}{
		{`{"duration_ms":881840}`, 881840},
		{`{"duration_ms":0}`, 0},
		{`{"duration_ms":-1}`, 0},
		{`{"duration_ms":999999999999}`, 0},
		{`{"duration_ms":"881840"}`, 0},
		{`{"duration_ms":1.5}`, 0},
		{`{`, 0},
	} {
		got := mcpMediaDurationMs(mcp_go.TextResourceContents{MIMEType: "application/json", Text: tc.text})
		if got != tc.want {
			t.Errorf("%s: duration=%d want=%d", tc.text, got, tc.want)
		}
	}
	if got := mcpMediaDurationMs(mcp_go.BlobResourceContents{MIMEType: "audio/mpeg", Blob: "AAAA"}); got != 0 {
		t.Fatal("legacy audio-only resource treated as metadata")
	}
	if got := mcpMediaDurationMs(mcp_go.TextResourceContents{MIMEType: "text/plain", Text: `{"duration_ms":1000}`}); got != 0 {
		t.Fatal("non-metadata text treated as duration")
	}
}
