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

func TestStartKeyboard_Rows(t *testing.T) {
	type button struct{ label, data string }
	rows := startKeyboard().InlineKeyboard

	tests := []struct {
		name string
		row  int
		want []button
	}{
		{
			name: "first row",
			row:  0,
			want: []button{
				{"📅 Today's problem", cbCmdToday},
				{"🗓 LeetCode daily", cbCmdDaily},
				{"⚙️ Setup", cbCmdSetup},
			},
		},
		{
			name: "last row",
			row:  len(rows) - 1,
			want: []button{
				{"🎚 Difficulty", cbCmdDifficulty},
				{"🛑 Unsubscribe", cbCmdUnsub},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := rows[tt.row]
			if len(row) != len(tt.want) {
				t.Fatalf("expected %d buttons, got %d", len(tt.want), len(row))
			}
			for i, btn := range row {
				if btn.Text != tt.want[i].label {
					t.Errorf("button %d label: got %q, want %q", i, btn.Text, tt.want[i].label)
				}
				if btn.CallbackData == nil || *btn.CallbackData != tt.want[i].data {
					t.Errorf("button %d data: got %v, want %q", i, btn.CallbackData, tt.want[i].data)
				}
			}
		})
	}
}
