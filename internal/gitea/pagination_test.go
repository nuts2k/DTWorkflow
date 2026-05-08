package gitea

import (
	"context"
	"fmt"
	"testing"
)

func TestPaginateAll_SinglePage(t *testing.T) {
	result, truncated, err := PaginateAll(context.Background(), 50, 10,
		func(_ context.Context, page, pageSize int) ([]string, *Response, error) {
			if page != 1 {
				t.Fatalf("只应请求第 1 页，实际请求第 %d 页", page)
			}
			return []string{"a", "b"}, &Response{NextPage: 0}, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
	if truncated {
		t.Fatal("single page should not be truncated")
	}
}

func TestPaginateAll_NilResponse(t *testing.T) {
	result, truncated, err := PaginateAll(context.Background(), 50, 10,
		func(_ context.Context, _, _ int) ([]string, *Response, error) {
			return []string{"x"}, nil, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}
	if truncated {
		t.Fatal("nil response should not be truncated")
	}
}

func TestPaginateAll_MultiPage(t *testing.T) {
	result, truncated, err := PaginateAll(context.Background(), 2, 10,
		func(_ context.Context, page, _ int) ([]int, *Response, error) {
			switch page {
			case 1:
				return []int{1, 2}, &Response{NextPage: 2}, nil
			case 2:
				return []int{3, 4}, &Response{NextPage: 3}, nil
			case 3:
				return []int{5}, &Response{NextPage: 0}, nil
			default:
				t.Fatalf("unexpected page %d", page)
				return nil, nil, nil
			}
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result))
	}
	if truncated {
		t.Fatal("multi page with terminal response should not be truncated")
	}
	for i, v := range result {
		if v != i+1 {
			t.Errorf("result[%d] = %d, want %d", i, v, i+1)
		}
	}
}

func TestPaginateAll_MaxPagesTruncation(t *testing.T) {
	calls := 0
	result, truncated, err := PaginateAll(context.Background(), 10, 3,
		func(_ context.Context, _, _ int) ([]string, *Response, error) {
			calls++
			return []string{"item"}, &Response{NextPage: calls + 1}, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result))
	}
	if !truncated {
		t.Fatal("expected truncated=true when maxPages reached with next page")
	}
}

func TestPaginateAll_ErrorDiscardsPartialData(t *testing.T) {
	result, truncated, err := PaginateAll(context.Background(), 10, 10,
		func(_ context.Context, page, _ int) ([]string, *Response, error) {
			if page == 1 {
				return []string{"ok"}, &Response{NextPage: 2}, nil
			}
			return nil, nil, fmt.Errorf("page 2 error")
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if result != nil {
		t.Fatalf("expected nil result on error, got %v", result)
	}
	if truncated {
		t.Fatal("error path should not report truncation")
	}
}

func TestPaginateAll_DefaultValues(t *testing.T) {
	var gotPageSize int
	calls := 0
	_, _, _ = PaginateAll(context.Background(), 0, 0,
		func(_ context.Context, _, pageSize int) ([]string, *Response, error) {
			gotPageSize = pageSize
			calls++
			return nil, nil, nil
		})
	if gotPageSize != DefaultPageSize {
		t.Errorf("default pageSize = %d, want %d", gotPageSize, DefaultPageSize)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}
