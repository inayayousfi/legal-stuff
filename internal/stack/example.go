package stack

import (
	"strings"

	"github.com/inayayousfi/legal-stuff/internal/app"
)

// ExampleEnv is the .env.example file: every setting setup writes, with example values.
func ExampleEnv(apps []*app.App) string {
	s := &Stack{Apps: apps}
	var out strings.Builder
	out.WriteString("# Example of the .env file that '" + Command + " setup' writes. Values here are examples only.\n")
	for _, setting := range s.settings() {
		out.WriteString(setting.Key + "=" + setting.Example + "\n")
	}
	return out.String()
}
