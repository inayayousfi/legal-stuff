package cli

import (
	"bufio"
	"unicode"
	"unicode/utf8"

	"github.com/inayayousfi/legal-stuff/internal/flow"
)

// ReadMasked reads characters until Enter, handling Backspace and Ctrl+C.
// Escape sequences such as arrow keys are ignored.
func ReadMasked(prompt string, in *bufio.Reader, write func(string)) (string, error) {
	var characters []rune
	write(prompt)
	for {
		r, _, err := in.ReadRune()
		if err != nil {
			write("\r\n")
			return "", flow.ErrCancelled
		}
		switch {
		case r == '\r' || r == '\n':
			write("\r\n")
			return string(characters), nil
		case r == 0x03:
			write("\r\n")
			return "", flow.ErrCancelled
		case r == '\b' || r == 0x7f:
			if len(characters) > 0 {
				characters = characters[:len(characters)-1]
				write("\b \b")
			}
		case r == 0x1b:
			skipEscape(in)
		case r == utf8.RuneError:
		case unicode.IsPrint(r):
			characters = append(characters, r)
			write("*")
		}
	}
}

// skipEscape drops the rest of a terminal escape sequence such as "[A".
func skipEscape(in *bufio.Reader) {
	if in.Buffered() == 0 {
		return
	}
	next, _, err := in.ReadRune()
	if err != nil || (next != '[' && next != 'O') {
		return
	}
	for in.Buffered() > 0 {
		r, _, err := in.ReadRune()
		if err != nil || (r >= 0x40 && r <= 0x7e) {
			return
		}
	}
}
