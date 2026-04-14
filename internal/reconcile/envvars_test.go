package reconcile

import (
	"strings"
	"testing"
)

func TestResolveEntry_LiteralPassthrough(t *testing.T) {
	k, v, err := resolveEntry("BEHIND_HTTPS_PROXY=True", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k != "BEHIND_HTTPS_PROXY" || v != "True" {
		t.Errorf("got %q=%q, want BEHIND_HTTPS_PROXY=True", k, v)
	}
}

func TestResolveEntry_SingleVar(t *testing.T) {
	sources := map[string]string{"MAIN_DB_URL": "postgresql://host/db"}
	k, v, err := resolveEntry("DATABASE_URL=$MAIN_DB_URL", sources)
	if err != nil {
		t.Fatal(err)
	}
	if k != "DATABASE_URL" || v != "postgresql://host/db" {
		t.Errorf("got %q=%q", k, v)
	}
}

func TestResolveEntry_BracedVar(t *testing.T) {
	sources := map[string]string{"MAIN_DB_URL": "postgresql://host/db"}
	k, v, err := resolveEntry("DB=${MAIN_DB_URL}", sources)
	if err != nil {
		t.Fatal(err)
	}
	if k != "DB" || v != "postgresql://host/db" {
		t.Errorf("got %q=%q", k, v)
	}
}

func TestResolveEntry_Composed(t *testing.T) {
	sources := map[string]string{"USER": "myuser", "PASS": "mypass"}
	k, v, err := resolveEntry("AUTH=$USER:$PASS", sources)
	if err != nil {
		t.Fatal(err)
	}
	if k != "AUTH" || v != "myuser:mypass" {
		t.Errorf("got %q=%q", k, v)
	}
}

func TestResolveEntry_BareName(t *testing.T) {
	// "SECRET_KEY" with no = → key=SECRET_KEY, value=SECRET_KEY (ParseSecretRef behavior)
	k, v, err := resolveEntry("SECRET_KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k != "SECRET_KEY" || v != "SECRET_KEY" {
		t.Errorf("got %q=%q, want SECRET_KEY=SECRET_KEY", k, v)
	}
}

func TestResolveEntry_AliasNoDollar(t *testing.T) {
	k, v, err := resolveEntry("SECRET_KEY=BUGSINK_SECRET_KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k != "SECRET_KEY" || v != "BUGSINK_SECRET_KEY" {
		t.Errorf("got %q=%q", k, v)
	}
}

func TestResolveEntry_AliasWithDollar(t *testing.T) {
	sources := map[string]string{"MAIN_DATABASE_URL": "pg://host/db"}
	k, v, err := resolveEntry("DB_URL=$MAIN_DATABASE_URL", sources)
	if err != nil {
		t.Fatal(err)
	}
	if k != "DB_URL" || v != "pg://host/db" {
		t.Errorf("got %q=%q", k, v)
	}
}

func TestResolveEntry_UnknownVar(t *testing.T) {
	_, _, err := resolveEntry("FOO=$NOPE", map[string]string{})
	if err == nil {
		t.Fatal("expected error for unknown $NOPE")
	}
	if !strings.Contains(err.Error(), "$NOPE") {
		t.Errorf("error should mention $NOPE: %v", err)
	}
}

func TestResolveEntry_DollarNotVar(t *testing.T) {
	// $100 — digit after $, not a var reference → literal passthrough
	k, v, err := resolveEntry("PRICE=$100", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k != "PRICE" || v != "$100" {
		t.Errorf("got %q=%q, want PRICE=$100", k, v)
	}
}

func TestResolveEntry_MixedLiteralAndVar(t *testing.T) {
	sources := map[string]string{"HOST": "example.com"}
	k, v, err := resolveEntry("URL=https://$HOST/api", sources)
	if err != nil {
		t.Fatal(err)
	}
	if k != "URL" || v != "https://example.com/api" {
		t.Errorf("got %q=%q", k, v)
	}
}

func TestResolveEntry_UnclosedBrace(t *testing.T) {
	_, _, err := resolveEntry("FOO=${UNCLOSED", nil)
	if err == nil {
		t.Fatal("expected error for unclosed ${")
	}
}

func TestResolveEntry_MultipleVars(t *testing.T) {
	sources := map[string]string{
		"PROTO": "https",
		"HOST":  "db.example.com",
		"PORT":  "5432",
		"DB":    "mydb",
	}
	k, v, err := resolveEntry("URL=$PROTO://$HOST:$PORT/$DB", sources)
	if err != nil {
		t.Fatal(err)
	}
	if k != "URL" || v != "https://db.example.com:5432/mydb" {
		t.Errorf("got %q=%q", k, v)
	}
}

func TestExtractVarRefs(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"EMAIL=$SMTP_USER", []string{"SMTP_USER"}},
		{"AUTH=$USER:$PASS", []string{"USER", "PASS"}},
		{"URL=${HOST}/path", []string{"HOST"}},
		{"plain=value", nil},
		{"$100", nil},
		{"CREATE=$A:$B", []string{"A", "B"}},
	}
	for _, tt := range tests {
		got := extractVarRefs(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("extractVarRefs(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("extractVarRefs(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestHasVarRef(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"plain", false},
		{"$VAR", true},
		{"${VAR}", true},
		{"prefix$VAR", true},
		{"$100", false},
		{"$$", false},
		{"", false},
		{"foo$", false},
	}
	for _, tt := range tests {
		got := hasVarRef(tt.input)
		if got != tt.want {
			t.Errorf("hasVarRef(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
