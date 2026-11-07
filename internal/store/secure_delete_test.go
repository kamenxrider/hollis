package store

import "testing"

func TestSecureDeleteEnabled(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	for i := 0; i < 3; i++ {
		var enabled int
		if err := st.db.QueryRow("PRAGMA secure_delete").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatalf("secure_delete=%d err=%v", enabled, err)
		}
	}
}
