package backend

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"
)

// MemoryBackend implements Backend with in-memory storage. Useful for testing.
type MemoryBackend struct {
	mu      sync.RWMutex
	objects map[string]map[string]*memObject
}

type memObject struct {
	data        []byte
	contentType string
	modTime     time.Time
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		objects: make(map[string]map[string]*memObject),
	}
}

func (m *MemoryBackend) GetObject(_ context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, ok := m.objects[bucket]
	if !ok {
		return nil, ObjectInfo{}, ErrNotFound
	}
	obj, ok := bkt[key]
	if !ok {
		return nil, ObjectInfo{}, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.data)), ObjectInfo{
		Key:          key,
		Size:         int64(len(obj.data)),
		ContentType:  obj.contentType,
		LastModified: obj.modTime,
	}, nil
}

func (m *MemoryBackend) PutObject(_ context.Context, bucket, key string, data io.Reader, _ int64, contentType string) error {
	body, err := io.ReadAll(data)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.objects[bucket]; !ok {
		m.objects[bucket] = make(map[string]*memObject)
	}
	m.objects[bucket][key] = &memObject{
		data:        body,
		contentType: contentType,
		modTime:     time.Now(),
	}
	return nil
}

func (m *MemoryBackend) RemoveObject(_ context.Context, bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if bkt, ok := m.objects[bucket]; ok {
		delete(bkt, key)
	}
	return nil
}

func (m *MemoryBackend) ListObjects(_ context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, ok := m.objects[bucket]
	if !ok {
		return nil, nil
	}

	var result []ObjectInfo
	for key, obj := range bkt {
		if strings.HasPrefix(key, prefix) {
			result = append(result, ObjectInfo{
				Key:          key,
				Size:         int64(len(obj.data)),
				ContentType:  obj.contentType,
				LastModified: obj.modTime,
			})
		}
	}
	return result, nil
}
