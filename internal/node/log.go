package node

import (
	"os"
	"sync"
)

type RotatingLog struct {
	mu   sync.Mutex
	Path string
}

func (l *RotatingLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s, err := os.Stat(l.Path); err == nil && s.Size()+int64(len(b)) > 2<<20 {
		if err = os.Rename(l.Path, l.Path+".1"); err != nil {
			return 0, err
		}
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if len(b) > 2<<20 {
		_, err = f.Write(b[len(b)-(2<<20):])
		return len(b), err
	}
	return f.Write(b)
}
