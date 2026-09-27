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

func TestDifficultyKeyboard(t *testing.T) {
	tests := []struct {
		name       string
		selected   []string
		wantLabels []string
	}{
		{
			name:       "nothing selected",
			selected:   nil,
			wantLabels: []string{"⬜ Easy", "⬜ Medium", "⬜ Hard"},
		},
		{
			name:       "all selected",
			selected:   []string{"Easy", "Medium", "Hard"},
			wantLabels: []string{"✅ Easy", "✅ Medium", "✅ Hard"},
		},
		{
			name:       "single selection",
			selected:   []string{"Medium"},
			wantLabels: []string{"⬜ Easy", "✅ Medium", "⬜ Hard"},
		},
		{
			name:       "unordered selection keeps canonical button order",
			selected:   []string{"Hard", "Easy"},
			wantLabels: []string{"✅ Easy", "⬜ Medium", "✅ Hard"},
		},
		{
			name:       "unknown values are ignored",
			selected:   []string{"Extreme"},
			wantLabels: []string{"⬜ Easy", "⬜ Medium", "⬜ Hard"},
		},
	}

	wantData := []string{"diff:Easy", "diff:Medium", "diff:Hard"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := difficultyKeyboard(tt.selected)

			if len(kb.InlineKeyboard) != 2 {
				t.Fatalf("expected 2 rows, got %d", len(kb.InlineKeyboard))
			}
			toggles := kb.InlineKeyboard[0]
			if len(toggles) != len(tt.wantLabels) {
				t.Fatalf("expected %d toggle buttons, got %d", len(tt.wantLabels), len(toggles))
			}
			for i, btn := range toggles {
				if btn.Text != tt.wantLabels[i] {
					t.Errorf("button %d label: got %q, want %q", i, btn.Text, tt.wantLabels[i])
				}
				if btn.CallbackData == nil || *btn.CallbackData != wantData[i] {
					t.Errorf("button %d data: got %v, want %q", i, btn.CallbackData, wantData[i])
				}
			}

			save := kb.InlineKeyboard[1]
			if len(save) != 1 || save[0].Text != "💾 Save" || save[0].CallbackData == nil || *save[0].CallbackData != cbCmdDiffSave {
				t.Errorf("save row: got %+v, want a single \"💾 Save\" button with data %q", save, cbCmdDiffSave)
			}
		})
	}
}

func TestStartKeyboard_LastRow(t *testing.T) {
	tests := []struct {
		name  string
		index int
		label string
		data  string
	}{
		{name: "difficulty", index: 0, label: "🎚 Difficulty", data: cbCmdDifficulty},
		{name: "unsubscribe", index: 1, label: "🛑 Unsubscribe", data: cbCmdUnsub},
	}

	rows := startKeyboard().InlineKeyboard
	last := rows[len(rows)-1]
	if len(last) != len(tests) {
		t.Fatalf("expected %d buttons in the last row, got %d", len(tests), len(last))
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			btn := last[tt.index]
			if btn.Text != tt.label {
				t.Errorf("label: got %q, want %q", btn.Text, tt.label)
			}
			if btn.CallbackData == nil || *btn.CallbackData != tt.data {
				t.Errorf("data: got %v, want %q", btn.CallbackData, tt.data)
			}
		})
	}
}
