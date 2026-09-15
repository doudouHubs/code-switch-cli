//go:build windows

package services

import "testing"

func TestPetKeyboardIsKeyDownMessage(t *testing.T) {
	tests := []struct {
		name   string
		wParam uintptr
		want   bool
	}{
		{name: "key down", wParam: petKeyboardMessageKeyDown, want: true},
		{name: "system key down", wParam: petKeyboardMessageSysKeyDown, want: true},
		{name: "key up", wParam: 0x0101, want: false},
		{name: "system key up", wParam: 0x0105, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := petKeyboardIsKeyDownMessage(test.wParam); got != test.want {
				t.Fatalf("petKeyboardIsKeyDownMessage(%#x) = %t, want %t", test.wParam, got, test.want)
			}
		})
	}
}
