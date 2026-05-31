package message_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"heckel.io/ntfy/v2/model"
)

func FuzzActionsJSONRoundTrip(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"action":"view","label":"Open","url":"https://example.com"}]`))
	f.Add([]byte(`[{"action":"http","label":"Ack","url":"https://x","method":"PUT","body":"ack=1","headers":{"X-Foo":"bar"}}]`))
	f.Add([]byte(`[{"action":"broadcast","label":"Cast","intent":"io.example.INTENT","extras":{"key1":"val1","key2":"val2"},"clear":true}]`))
	f.Add([]byte(`[{"action":"view","label":"Open","url":"https://example.com"},{"action":"http","label":"Ack","url":"https://x","method":"PUT","body":"ack=1","headers":{"X-Foo":"bar"}},{"action":"broadcast","label":"Cast","intent":"io.example.INTENT","extras":{"key1":"val1","key2":"val2"},"clear":true},{"action":"copy","label":"Copy","value":"code-123"},{"action":"view","label":"Docs","url":"https://docs.example.com","clear":false}]`))
	f.Add([]byte(`[{"action":"http","label":"Nested strings","url":"https://x","headers":{"L1":"{\"L2\":{\"L3\":\"header\"}}"},"extras":{"L1":"{\"L2\":{\"L3\":\"extra\"}}"}}]`))
	f.Add([]byte(`[{"action":"view","label":"打开 🚀","url":"https://例え.jp/"}]`))
	f.Add([]byte(`[{"action":"http","label":"` + strings.Repeat("L", 4096) + `","url":"https://example.com/` + strings.Repeat("u", 4096) + `","method":"POST","body":"` + strings.Repeat("B", 4096) + `","headers":{"X-Long":"` + strings.Repeat("H", 4096) + `"}}]`))
	f.Add([]byte(`[null]`))
	f.Add([]byte(`[{}]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var actions []*model.Action
		if err := json.Unmarshal(data, &actions); err != nil {
			t.Skip()
		}

		// Invariant: any action list that the cache read path can unmarshal must be encodable again.
		re, err := json.Marshal(actions)
		if err != nil {
			t.Fatalf("remarshal after successful unmarshal failed: %v", err)
		}

		var actions2 []*model.Action
		// Invariant: JSON produced by the cache write shape must be accepted by the cache read shape.
		if err := json.Unmarshal(re, &actions2); err != nil {
			t.Fatalf("unmarshal of remarshalled actions failed: %v\njson=%s", err, re)
		}

		// Invariant: marshal -> unmarshal -> marshal reaches a canonical fixed point and loses no JSON-visible fields.
		re2, err := json.Marshal(actions2)
		if err != nil {
			t.Fatalf("second marshal failed: %v", err)
		}
		if !bytes.Equal(re, re2) {
			t.Fatalf("actions JSON round-trip was not a fixed point\nfirst:  %s\nsecond: %s", re, re2)
		}
	})
}

func FuzzMessageHeaderFieldsRoundTrip(f *testing.F) {
	f.Add("", "", "", "", "", "")
	f.Add("Title", "Message", "https://example.com", "https://example.com/icon.png", "tag1,tag2", "topic")
	f.Add("标题 🚀", "消息正文 😀", "https://例え.jp/打开", "https://例え.jp/icon.png", "通知,🚀", "主题")
	f.Add("quote \" slash \\ newline\n nul "+string(rune(0))+" ctrl "+string(rune(1)), "body \" \\ \r\n "+string(rune(0))+string(rune(2)), "https://x/\"\\", "icon\n"+string(rune(0)), "tag\"one,tag\\two", "topic\n"+string(rune(0)))
	f.Add(strings.Repeat("T", 4096), strings.Repeat("M", 4096), "https://example.com/"+strings.Repeat("c", 4096), "https://example.com/"+strings.Repeat("i", 4096), strings.Repeat("tag", 1366), strings.Repeat("topic", 819))
	f.Add(`\u0000`, `literal "\u0000" plus actual `+string(rune(0)), "https://example.com/?q=\\u0000", "icon-\\u0000", "\\u0000", "topic-\\u0000")

	f.Fuzz(func(t *testing.T, title, message, click, icon, tags, topic string) {
		if !utf8.ValidString(title) || !utf8.ValidString(message) || !utf8.ValidString(click) || !utf8.ValidString(icon) || !utf8.ValidString(tags) || !utf8.ValidString(topic) {
			t.Skip()
		}

		m := model.NewDefaultMessage(topic, message)
		m.Title = title
		m.Click = click
		m.Icon = icon
		m.Tags = strings.Split(tags, ",")

		// Invariant: valid UTF-8 message header/body strings must be JSON encodable without data loss.
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal message failed: %v", err)
		}

		var decoded model.Message
		// Invariant: message JSON produced for storage/API use must decode back into the message shape.
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal message failed: %v\njson=%s", err, encoded)
		}

		// Invariant: JSON round-trip must preserve every fuzzed string field byte-for-byte.
		if m.Title != decoded.Title || m.Message != decoded.Message || m.Click != decoded.Click || m.Icon != decoded.Icon || !stringSlicesEqual(m.Tags, decoded.Tags) || m.Topic != decoded.Topic {
			t.Fatalf("message JSON round-trip changed fields\nbefore: title=%q message=%q click=%q icon=%q tags=%q topic=%q\nafter:  title=%q message=%q click=%q icon=%q tags=%q topic=%q", m.Title, m.Message, m.Click, m.Icon, m.Tags, m.Topic, decoded.Title, decoded.Message, decoded.Click, decoded.Icon, decoded.Tags, decoded.Topic)
		}
	})
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
