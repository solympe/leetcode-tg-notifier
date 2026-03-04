package bot

import (
	"strings"
	"testing"
)

func TestSetupTimeKeyboard_ButtonCount(t *testing.T) {
	kb := setupTimeKeyboard()
	total := 0
	for _, row := range kb.InlineKeyboard {
		total += len(row)
	}
	if total != 8 {
		t.Errorf("expected 8 buttons, got %d", total)
	}
}

func TestSetupTzKeyboard_ButtonCount(t *testing.T) {
	kb := setupTzKeyboard()
	total := 0
	for _, row := range kb.InlineKeyboard {
		total += len(row)
	}
	if total != 7 {
		t.Errorf("expected 7 buttons, got %d", total)
	}
}

func TestTzLabel_WithMinutes(t *testing.T) {
	label := tzLabel("Kolkata", "Asia/Kolkata")
	// Asia/Kolkata is UTC+5:30
	if !strings.Contains(label, ":30") {
		t.Errorf("expected ':30' in label for Asia/Kolkata, got %q", label)
	}
}
