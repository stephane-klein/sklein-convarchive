package mattermost

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// postBase is the CreateAt of the oldest post in newPagedPostsServer.
const postBase = 1_000_000

// newPagedPostsServer returns a server simulating the channel posts endpoint:
// page 0 holds the newest posts, each page's order is descending (newest
// first), and an out-of-range page yields an empty list. Post at global offset
// i has CreateAt = postBase + (total-1-i), so offset 0 is the newest post.
func newPagedPostsServer(t *testing.T, total, perPage int, reqs *int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page := 0
		fmt.Sscanf(q.Get("page"), "%d", &page)
		per := 0
		fmt.Sscanf(q.Get("per_page"), "%d", &per)

		mu.Lock()
		*reqs++
		mu.Unlock()

		start := page * per
		if start >= total {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"order":[],"posts":{}}`)
			return
		}
		end := start + per
		if end > total {
			end = total
		}
		list := PostList{Order: []string{}, Posts: map[string]*Post{}}
		for i := start; i < end; i++ {
			id := fmt.Sprintf("post%d", i)
			list.Order = append(list.Order, id)
			list.Posts[id] = &Post{Id: id, CreateAt: postBase + int64(total-1-i)}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	}))
}

func newTokenClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client := NewClient(baseURL)
	if err := client.Authenticate(t.Context(), AuthConfig{Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	return client
}

func TestGetOldestPost(t *testing.T) {
	const (
		total   = 450
		perPage = 200
	)

	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, total, perPage, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	oldest, err := client.GetOldestPost(t.Context(), "ch", perPage)
	if err != nil {
		t.Fatal(err)
	}
	if oldest == nil {
		t.Fatal("expected an oldest post")
	}
	if oldest.Id != "post449" {
		t.Fatalf("got %q, want post449", oldest.Id)
	}

	mu.Lock()
	gotReqs := reqs
	mu.Unlock()
	if gotReqs > 20 {
		t.Fatalf("binary search used %d requests, want <= ~20", gotReqs)
	}
}

func TestGetOldestPostSinglePage(t *testing.T) {
	const (
		total   = 50
		perPage = 200
	)

	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, total, perPage, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	oldest, err := client.GetOldestPost(t.Context(), "ch", perPage)
	if err != nil {
		t.Fatal(err)
	}
	if oldest == nil || oldest.Id != "post49" {
		t.Fatalf("got %+v, want post49", oldest)
	}
}

func TestGetOldestPostEmptyChannel(t *testing.T) {
	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, 0, 200, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	oldest, err := client.GetOldestPost(t.Context(), "ch", 200)
	if err != nil {
		t.Fatal(err)
	}
	if oldest != nil {
		t.Fatalf("expected nil, got %+v", oldest)
	}
}

func TestPostsAscendingOrder(t *testing.T) {
	const (
		total   = 450
		perPage = 200
	)

	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, total, perPage, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	var visited []*Post
	pages := 0
	err := client.PostsAscending(t.Context(), "ch", 0, 0, perPage, func() error {
		pages++
		return nil
	}, func(p *Post) error {
		visited = append(visited, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(visited) != total {
		t.Fatalf("visited %d posts, want %d", len(visited), total)
	}
	if pages != 3 {
		t.Fatalf("pageDone called %d times, want 3", pages)
	}
	if visited[0].Id != "post449" || visited[len(visited)-1].Id != "post0" {
		t.Fatalf("traversal = %s..%s, want post449..post0", visited[0].Id, visited[len(visited)-1].Id)
	}
	for i := 1; i < len(visited); i++ {
		if visited[i].CreateAt <= visited[i-1].CreateAt {
			t.Fatalf("posts not strictly ascending at %d: %d <= %d", i, visited[i].CreateAt, visited[i-1].CreateAt)
		}
	}
}

func TestPostsAscendingPeriodBounds(t *testing.T) {
	const (
		total   = 450
		perPage = 200
	)
	// Post i has CreateAt = postBase + (total-1-i). The range
	// [postBase+100, postBase+300] maps to offsets 149..349 (201 posts).
	from := int64(postBase) + 100
	until := int64(postBase) + 300

	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, total, perPage, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	var visited []string
	err := client.PostsAscending(t.Context(), "ch", from, until, perPage, nil, func(p *Post) error {
		visited = append(visited, p.Id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(visited) != 201 {
		t.Fatalf("visited %d posts, want 201", len(visited))
	}
	if visited[0] != "post349" {
		t.Errorf("first visited = %s, want post349 (oldest in range)", visited[0])
	}
	if visited[len(visited)-1] != "post149" {
		t.Errorf("last visited = %s, want post149 (newest in range)", visited[len(visited)-1])
	}
}

func TestPostsAscendingEmptyChannel(t *testing.T) {
	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, 0, 200, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	visited := 0
	if err := client.PostsAscending(t.Context(), "ch", 0, 0, 200, nil, func(p *Post) error {
		visited++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited != 0 {
		t.Fatalf("visited %d posts, want 0", visited)
	}
}

func TestPostsAscendingFnError(t *testing.T) {
	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, 50, 200, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	err := client.PostsAscending(t.Context(), "ch", 0, 0, 200, nil, func(p *Post) error {
		return fmt.Errorf("boom")
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got error %v, want boom", err)
	}
}

func TestPostsAscendingPageDoneError(t *testing.T) {
	var mu sync.Mutex
	reqs := 0
	srv := newPagedPostsServer(t, 50, 200, &reqs, &mu)
	defer srv.Close()

	client := newTokenClient(t, srv.URL)
	err := client.PostsAscending(t.Context(), "ch", 0, 0, 200, func() error {
		return fmt.Errorf("page fail")
	}, func(p *Post) error {
		return nil
	})
	if err == nil || err.Error() != "page fail" {
		t.Fatalf("got error %v, want page fail", err)
	}
}
