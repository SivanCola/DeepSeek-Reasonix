package persistentshell

import "testing"

func TestExtractOutputIgnoresEchoedScript(t *testing.T) {
	start := "REASONIX_START_abc"
	end := "REASONIX_END_abc"
	raw := "printf '%s\\n' '" + start + "'; eval 'pwd'\n" +
		"printf '%s%d\\n' '" + end + "' $?\n" +
		start + "\n" +
		"/tmp/work\n" +
		end + "0\n"
	body, code, ok := extractOutput(raw, start, end)
	if !ok {
		t.Fatal("expected completed extraction")
	}
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if body != "/tmp/work" {
		t.Fatalf("body=%q", body)
	}
}

func TestExtractOutputCRLF(t *testing.T) {
	start := "REASONIX_START_x"
	end := "REASONIX_END_x"
	raw := start + "\r\nhello\r\n" + end + "7\r\n"
	body, code, ok := extractOutput(raw, start, end)
	if !ok || code != 7 || body != "hello" {
		t.Fatalf("body=%q code=%d ok=%v", body, code, ok)
	}
}

func TestHasReady(t *testing.T) {
	if hasReady("noise\n" + readyToken + "\nmore") {
		return
	}
	t.Fatal("ready token not detected")
}

func TestPosixQuote(t *testing.T) {
	if got := posixQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("got %q", got)
	}
}
