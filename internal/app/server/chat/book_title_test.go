package chat

import "testing"

func TestSplitBookTitle(t *testing.T) {
	cases := []struct {
		in            string
		book, chapter string
		ok            bool
	}{
		{"《天黑了，我想睡觉》 第二百七十八章 心藏剑种，俗世无风", "天黑了，我想睡觉", "第二百七十八章 心藏剑种，俗世无风", true},
		{"《无敌天命》第一千八百零一章：就算四剑来！", "无敌天命", "第一千八百零一章：就算四剑来！", true},
		{"晴天", "", "", false},
		{"《》第一章", "", "", false},
		{"《没有结尾", "", "", false},
	}
	for _, c := range cases {
		book, chapter, ok := splitBookTitle(c.in)
		if book != c.book || chapter != c.chapter || ok != c.ok {
			t.Errorf("splitBookTitle(%q) = %q, %q, %v; want %q, %q, %v", c.in, book, chapter, ok, c.book, c.chapter, c.ok)
		}
	}
}
