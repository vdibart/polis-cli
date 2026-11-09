package secret

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"text/template"
)

const planted = "hunter2-SECRET"

// ⛔⛔ A Value never prints its secret — under ANY verb, and not when it sits
// in an unexported field of a struct someone prints. The previous version
// leaked under %d %t %e %c %U %b %o %p and through nested unexported fields.
func TestAValueNeverPrintsItsSecret(t *testing.T) {
	v := New(planted)
	p := &v
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%t", "%e", "%E", "%f", "%g", "%c", "%U", "%b", "%o", "%O", "%p", "%T", "%10.3s", "%-8d"} {
		for _, arg := range []any{v, p} {
			if out := fmt.Sprintf(verb, arg); strings.Contains(out, planted) {
				t.Errorf("%s of %T leaked: %s", verb, arg, out)
			}
		}
	}
	type holder struct {
		name string
		dsn  Value
		ptr  *Value
	}
	h := holder{name: "n", dsn: v, ptr: p}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d"} {
		if out := fmt.Sprintf(verb, h); strings.Contains(out, planted) {
			t.Errorf("%s of a struct with a Value in an unexported field leaked: %s", verb, out)
		}
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "h", h, "v", v)
	if strings.Contains(buf.String(), planted) {
		t.Errorf("slog text leaked: %s", buf.String())
	}
	tpl := template.Must(template.New("t").Parse(`{{printf "%d" .}} {{.}}`))
	buf.Reset()
	_ = tpl.Execute(&buf, v)
	if strings.Contains(buf.String(), planted) {
		t.Errorf("text/template leaked: %s", buf.String())
	}
	if s := reflect.ValueOf(h).Field(1).Field(0).String(); strings.Contains(s, planted) {
		t.Errorf("reflection on the nested field leaked: %s", s)
	}
	if v.Expose() != planted {
		t.Error("Expose no longer returns the value")
	}
}
