package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestApp_LeavesUsageErrorsToMain(t *testing.T) {
	cases := map[string]string{
		"vsenv --bogus":           "vsenv",
		"vsenv env":               "vsenv env",
		"vsenv env --shell":       "vsenv env",
		"vsenv exec --bogus -- x": "vsenv exec",
	}
	for args, cmd := range cases {
		var out bytes.Buffer
		app := newApp()
		app.Writer, app.ErrWriter = &out, &out
		err := app.Run(context.Background(), strings.Fields(args))
		hint := ` (see "` + cmd + ` --help")`
		if err == nil || !strings.HasSuffix(err.Error(), hint) || out.Len() > 0 {
			t.Errorf("%s: got error %v and output %q, want an error ending in %s and no output",
				args, err, out.String(), hint)
		}
	}
}
