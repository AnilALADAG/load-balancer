package main

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
)

func createTestBackend(t *testing.T, rawUrl string) *Backend {
	// rawUrl'yi URL olarak çözümleme
	parsedUrl, err := url.Parse(rawUrl)
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}

	// Test sunucusunu oluşturma
	proxy := httputil.NewSingleHostReverseProxy(parsedUrl)

	// ReverseProxy'yi oluşturmak için proxy.URL kullanılıyor
	// proxy.URL, *url.URL tipindedir ve bu nedenle doğrudan kullanılabilir
	return &Backend{
		URL:          parsedUrl,
		Alive:        true,
		ReverseProxy: proxy, // Doğru kullanım
	}
}

// Test if the server pool correctly adds backends
func TestAddBackend(t *testing.T) {
	serverPool := &ServerPool{}
	backend1 := createTestBackend(t, "http://localhost:3031")
	backend2 := createTestBackend(t, "http://localhost:3032")

	serverPool.AddBackend(backend1)
	serverPool.AddBackend(backend2)

	if len(serverPool.backends) != 2 {
		t.Errorf("Expected 2 backends, got %d", len(serverPool.backends))
	}
}

// Test that the server pool cycles through backends correctly
func TestNextIndex(t *testing.T) {
	serverPool := &ServerPool{}
	backend1 := createTestBackend(t, "http://localhost:3031")
	backend2 := createTestBackend(t, "http://localhost:3032")

	serverPool.AddBackend(backend1)
	serverPool.AddBackend(backend2)

	// First request
	idx1 := serverPool.NextIndex()
	// Second request
	idx2 := serverPool.NextIndex()

	if idx1 == idx2 {
		t.Errorf("Expected different backend indices but got the same: %d", idx1)
	}
}

// Test that the server pool correctly updates backend status
func TestMarkBackendStatus(t *testing.T) {
	serverPool := &ServerPool{}
	backend := createTestBackend(t, "http://localhost:3031")

	serverPool.AddBackend(backend)
	serverPool.MarkBackendStatus(backend.URL, false)

	if backend.IsAlive() {
		t.Errorf("Expected backend to be marked as down, but it's up")
	}
}

// Test GetNextPeer returns the next alive backend
func TestGetNextPeer(t *testing.T) {
	serverPool := &ServerPool{}
	backend1 := createTestBackend(t, "http://localhost:3031")
	backend2 := createTestBackend(t, "http://localhost:3032")

	backend2.SetAlive(false) // Mark one backend as down
	serverPool.AddBackend(backend1)
	serverPool.AddBackend(backend2)

	peer := serverPool.GetNextPeer()
	if peer == nil {
		t.Fatal("Expected to get a live backend, got nil")
	}
	if peer != backend1 {
		t.Errorf("Expected backend1, got %v", peer.URL)
	}
}

// Test that no peer is returned if all backends are down
func TestNoLivePeer(t *testing.T) {
	serverPool := &ServerPool{}
	backend1 := createTestBackend(t, "http://localhost:3031")
	backend2 := createTestBackend(t, "http://localhost:3032")

	backend1.SetAlive(false)
	backend2.SetAlive(false)

	serverPool.AddBackend(backend1)
	serverPool.AddBackend(backend2)

	peer := serverPool.GetNextPeer()
	if peer != nil {
		t.Errorf("Expected nil, got %v", peer.URL)
	}
}

// Test retry mechanism in reverse proxy error handler
func TestProxyRetry(t *testing.T) {
	// Create a server that will fail the first request
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Backend Error", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	parsedUrl, _ := url.Parse(failServer.URL)
	backend := &Backend{
		URL:          parsedUrl,
		Alive:        true,
		ReverseProxy: httputil.NewSingleHostReverseProxy(parsedUrl),
	}

	serverPool.AddBackend(backend)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	// Call the load balancer handler to test the retry logic
	lb(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected status code %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}
}

// Test that health check correctly updates the status of backends
func TestHealthCheck(t *testing.T) {
	serverPool := &ServerPool{}
	backend1 := createTestBackend(t, "http://localhost:3031")
	backend2 := createTestBackend(t, "http://localhost:3032")

	serverPool.AddBackend(backend1)
	serverPool.AddBackend(backend2)

	// Initially both backends are alive
	serverPool.HealthCheck()

	if !backend1.IsAlive() || !backend2.IsAlive() {
		t.Errorf("Expected both backends to be alive after health check")
	}
}

// Test that atomic increment works correctly for round-robin balancing
func TestAtomicIncrement(t *testing.T) {
	serverPool := &ServerPool{}
	backend1 := createTestBackend(t, "http://localhost:3031")
	backend2 := createTestBackend(t, "http://localhost:3032")

	serverPool.AddBackend(backend1)
	serverPool.AddBackend(backend2)

	// Simulate multiple increments
	for i := 0; i < 999; i++ {
		serverPool.NextIndex()
	}

	// 1000 çağrısı sonucunda s.current değeri 1000 olmalıdır, ama 0 döndürmelidir.
	expectedCurrent := uint64(1000 % 2) // Eğer 2 backend varsa
	if uint64(serverPool.NextIndex()) != expectedCurrent {
		t.Errorf("Expected current index %d, got %d", expectedCurrent, atomic.LoadUint64(&serverPool.current))
	}

}
