package router

import (
	"reflect"
	"testing"

	"guise/internal/config"
)

func rw(id, find, replace string, enabled, delayed bool) config.Rewrite {
	return config.Rewrite{ID: id, Enabled: enabled, Find: find, Replace: replace, Delayed: delayed}
}

func TestApplyRewritesHostSwap(t *testing.T) {
	rewrites := []config.Rewrite{rw("x", "x.com", "xcancel.com", true, false)}
	got, applied := ApplyRewrites(rewrites, "https://x.com/foo/bar", false)
	if got != "https://xcancel.com/foo/bar" {
		t.Errorf("got %q, want host swapped", got)
	}
	if !reflect.DeepEqual(applied, []string{"x"}) {
		t.Errorf("applied = %v, want [x]", applied)
	}
}

func TestApplyRewritesChainsInOrder(t *testing.T) {
	// Each rewrite operates on the previous one's output: swap host, then strip a
	// tracking param. Both fire.
	rewrites := []config.Rewrite{
		rw("host", "x.com", "xcancel.com", true, false),
		rw("track", "?utm_source=foo", "", true, false),
	}
	got, applied := ApplyRewrites(rewrites, "https://x.com/p?utm_source=foo", false)
	if got != "https://xcancel.com/p" {
		t.Errorf("got %q, want chained result", got)
	}
	if !reflect.DeepEqual(applied, []string{"host", "track"}) {
		t.Errorf("applied = %v, want [host track]", applied)
	}
}

func TestApplyRewritesPathRewrite(t *testing.T) {
	// Find/replace is not limited to the host — any substring works.
	rewrites := []config.Rewrite{rw("p", "/old/", "/new/", true, false)}
	got, _ := ApplyRewrites(rewrites, "https://h.com/old/page", false)
	if got != "https://h.com/new/page" {
		t.Errorf("got %q, want path rewritten", got)
	}
}

func TestApplyRewritesFiltersByDelayed(t *testing.T) {
	rewrites := []config.Rewrite{
		rw("pre", "a", "A", true, false),
		rw("post", "b", "B", true, true),
	}
	// The pre pass touches only the non-delayed rewrite.
	pre, preIDs := ApplyRewrites(rewrites, "ab", false)
	if pre != "Ab" || !reflect.DeepEqual(preIDs, []string{"pre"}) {
		t.Errorf("pre pass: got %q %v, want \"Ab\" [pre]", pre, preIDs)
	}
	// The post pass touches only the delayed rewrite.
	post, postIDs := ApplyRewrites(rewrites, pre, true)
	if post != "AB" || !reflect.DeepEqual(postIDs, []string{"post"}) {
		t.Errorf("post pass: got %q %v, want \"AB\" [post]", post, postIDs)
	}
}

func TestApplyRewritesSkipsDisabled(t *testing.T) {
	rewrites := []config.Rewrite{rw("off", "x.com", "xcancel.com", false, false)}
	got, applied := ApplyRewrites(rewrites, "https://x.com/p", false)
	if got != "https://x.com/p" || applied != nil {
		t.Errorf("disabled rewrite should be inert: got %q %v", got, applied)
	}
}

func TestApplyRewritesSkipsBlankFind(t *testing.T) {
	// A blank Find would otherwise splice Replace between every character.
	rewrites := []config.Rewrite{rw("blank", "", "INJECT", true, false)}
	got, applied := ApplyRewrites(rewrites, "abc", false)
	if got != "abc" || applied != nil {
		t.Errorf("blank find should be inert: got %q %v", got, applied)
	}
}

func TestApplyRewritesNoOccurrenceNotReported(t *testing.T) {
	// A Find that does not occur leaves the URL untouched and is not reported as
	// applied, so the log only lists rewrites that actually fired.
	rewrites := []config.Rewrite{rw("miss", "y.com", "z.com", true, false)}
	got, applied := ApplyRewrites(rewrites, "https://x.com/p", false)
	if got != "https://x.com/p" || applied != nil {
		t.Errorf("non-matching rewrite should be a no-op: got %q %v", got, applied)
	}
}

func TestApplyRewritesReplacesAllOccurrences(t *testing.T) {
	rewrites := []config.Rewrite{rw("all", "a", "_", true, false)}
	got, _ := ApplyRewrites(rewrites, "a/a/a", false)
	if got != "_/_/_" {
		t.Errorf("got %q, want every occurrence replaced", got)
	}
}
