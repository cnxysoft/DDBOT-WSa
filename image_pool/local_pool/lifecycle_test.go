package local_pool

import "testing"

func TestPreparedPoolLifecycle(t *testing.T) {
	p, err := PrepareLocalPool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.started {
		t.Fatal("prepared pool started background work")
	}
	p.Start()
	p.Start()
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	p.Start()
	if !p.closed {
		t.Fatal("retired pool restarted")
	}
}
