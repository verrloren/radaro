package store

import "testing"

func TestSourceSettings(t *testing.T) {
	st := openTest(t)
	if got, err := st.SourceSettings(); err != nil || len(got) != 0 {
		t.Fatalf("empty settings %v %v", got, err)
	}
	if err := st.SaveSourceSettings("x", map[string]string{"bearer_token": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSourceSettings("x", map[string]string{"bearer_token": "b"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.SourceSettings()
	if err != nil || got["x"]["bearer_token"] != "b" || len(got) != 1 {
		t.Fatalf("settings %v %v", got, err)
	}
	if ok, _ := st.DeleteSourceSettings("x"); !ok {
		t.Fatal("not deleted")
	}
	if ok, _ := st.DeleteSourceSettings("x"); ok {
		t.Fatal("deleted twice")
	}
}
