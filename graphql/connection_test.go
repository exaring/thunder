package graphql_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/samsarahq/go/snapshotter"
	"github.com/samsarahq/thunder/graphql"
	"github.com/samsarahq/thunder/graphql/schemabuilder"
	"github.com/samsarahq/thunder/reactive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type User struct {
	Name     string
	Age      int
	resource *reactive.Resource
}

type Slow struct {
}

type Args struct {
	Additional string
}

type Item struct {
	Id int64
}

type Snapshotter struct {
	*snapshotter.Snapshotter
	T           *testing.T
	Executor    graphql.Executor
	Schema      *graphql.Schema
	RecordError bool
}

func (s *Snapshotter) SnapshotQuery(name, query string) {
	q := graphql.MustParse(query, nil)
	require.NoError(s.T, graphql.PrepareQuery(s.Schema.Query, q.SelectionSet))
	output, err := s.Executor.Execute(context.Background(), s.Schema.Query, nil, q)

	if err != nil && s.RecordError {
		s.Snapshot(name, struct{ Error string }{err.Error()})
	} else {
		require.NoError(s.T, err)
		s.Snapshot(name, output)
	}
}

func TestConnection(t *testing.T) {
	schema := schemabuilder.NewSchema()
	type Inner struct {
	}

	query := schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner := schema.Object("inner", Inner{})
	item := schema.Object("item", Item{})
	item.Key("id")
	inner.FieldFunc("innerConnection", func(args Args) []Item {
		retList := make([]Item, 5)
		retList[0] = Item{Id: 1}
		retList[1] = Item{Id: 2}
		retList[2] = Item{Id: 3}
		retList[3] = Item{Id: 4}
		retList[4] = Item{Id: 5}
		return retList
	}, schemabuilder.Paginated)
	inner.FieldFunc("innerConnectionNilArg", func() []Item {
		retList := make([]Item, 5)
		retList[0] = Item{Id: 1}
		retList[1] = Item{Id: 2}
		retList[2] = Item{Id: 3}
		retList[3] = Item{Id: 4}
		retList[4] = Item{Id: 5}
		return retList
	}, schemabuilder.Paginated)
	inner.FieldFunc("innerConnectionWithCtxAndError", func(ctx context.Context, args Args) ([]Item, error) {
		retList := make([]Item, 5)
		retList[0] = Item{Id: 1}
		retList[1] = Item{Id: 2}
		retList[2] = Item{Id: 3}
		retList[3] = Item{Id: 4}
		retList[4] = Item{Id: 5}
		return retList, nil
	}, schemabuilder.Paginated)
	inner.FieldFunc("innerConnectionWithError", func(ctx context.Context, args Args) ([]*Item, error) {
		return nil, graphql.NewSafeError("this is an error")
	}, schemabuilder.Paginated)
	builtSchema := schema.MustBuild()

	snap := Snapshotter{
		Snapshotter: snapshotter.New(t),
		T:           t,
		Schema:      builtSchema,
	}
	defer snap.Verify()

	snap.SnapshotQuery("Pagination, first + after", `{
		inner {
			innerConnection(first: 1, after: "", additional: "jk") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
				}
			}
		}
	}`)

	snap.SnapshotQuery("Pagination, last + before", `{
		inner {
			innerConnection(last: 2, before: "", additional: "jk") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
					pages
				}
			}
		}
	}`)

	snap.SnapshotQuery("Pagination, no args given", `{
		inner {
			innerConnection(additional: "jk") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
					pages
				}
			}
		}
	}`)

	snap.SnapshotQuery("Pagination, nil args", `{
		inner {
			innerConnectionNilArg(first: 1, after: "") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
				}
			}
		}
	}`)

	snap.SnapshotQuery("Pagination, with ctx and error", `{
		inner {
			innerConnectionWithCtxAndError(first: 1, after: "", additional: "jk") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
				}
			}
		}
	}`)

	snap.RecordError = true
	snap.SnapshotQuery("Pagination, with ctx and error", `{
		inner {
			innerConnectionWithError(first: 1, after: "", additional: "jk") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
				}
			}
		}
	}`)

	snap.SnapshotQuery("Pagination, with error", `{
		inner {
			innerConnectionWithError(first: 1, after: "", additional: "jk") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
				}
			}
		}
	}`)

	snap.SnapshotQuery("Pagination, with error", `{
		inner {
			innerConnection(last: -2, before: "", additional: "jk") {
				totalCount
				edges {
					node {
						id
					}
					cursor
				}
				pageInfo {
					hasNextPage
					hasPrevPage
					startCursor
					endCursor
					pages
				}
			}
		}
	}`)
}

func TestPaginateBuildFailure(t *testing.T) {
	badMethodStr := "bad method inner on type schemabuilder.query:"

	schema := schemabuilder.NewSchema()
	type Inner struct {
	}

	query := schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner := schema.Object("inner", Inner{})
	item := schema.Object("item", Item{})
	item.Key("id")

	inner.PaginateFieldFunc("innerConnectionWithCtxAndError", func(ctx context.Context, args Args) (*Item, error) {
		return nil, nil
	})
	_, err := schema.Build()
	if err == nil || err.Error() != fmt.Sprintf("%v paginated field func must return a slice type", badMethodStr) {
		t.Errorf("bad error: %v", err)
	}

	schema = schemabuilder.NewSchema()
	query = schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner = schema.Object("inner", Inner{})
	item = schema.Object("item", Item{})

	inner.PaginateFieldFunc("innerConnectionWithCtxAndError", func(ctx context.Context, args Args) ([]Item, error) {
		return nil, nil
	})
	_, err = schema.Build()
	if err == nil || err.Error() != fmt.Sprintf("%v a key field must be registered for paginated objects", badMethodStr) {
		t.Errorf("bad error: %v", err)
	}

	schema = schemabuilder.NewSchema()
	type StructWithKey struct {
		Id int64
	}
	query = schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner = schema.Object("inner", Inner{})
	object := schema.Object("structWithKey", StructWithKey{})
	object.Key("wrongField")
	inner.PaginateFieldFunc("innerConnectionWithWrongKey", func(ctx context.Context, args Args) ([]StructWithKey, error) {
		return nil, nil
	})
	_, err = schema.Build()
	if err == nil || err.Error() != fmt.Sprintf("%v key field doesn't exist on object", badMethodStr) {
		t.Errorf("bad error: %v", err)
	}

	schema = schemabuilder.NewSchema()
	query = schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

}

func TestPaginateNodeTypeFailure(t *testing.T) {
	schema := schemabuilder.NewSchema()
	query := schema.Query()

	type Inner struct {
	}

	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner := schema.Object("inner", Inner{})

	inner.PaginateFieldFunc("innerConnectionWithScalar", func(ctx context.Context, args Args) ([]Item, error) {
		return nil, nil
	})

	badMethodStr := "bad method inner on type schemabuilder.query:"
	_, err := schema.Build()
	if err == nil || err.Error() != fmt.Sprintf("%s graphql_test.Item must be a struct and registered as an object along with its key", badMethodStr) {
		t.Errorf("bad error: %v", err)
	}

	schema = schemabuilder.NewSchema()
	query = schema.Query()

	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner = schema.Object("inner", Inner{})

	inner.PaginateFieldFunc("innerConnectionWithScalar", func(ctx context.Context, args Args) ([]string, error) {
		return nil, nil
	})

	badMethodStr = "bad method inner on type schemabuilder.query:"
	_, err = schema.Build()
	if err == nil || err.Error() != fmt.Sprintf("%s string must be a struct and registered as an object along with its key", badMethodStr) {
		t.Errorf("bad error: %v", err)
	}

}

type EmbeddedArgs struct {
	schemabuilder.PaginationArgs
	Additional string
}

func TestEmbeddedArgs(t *testing.T) {
	schema := schemabuilder.NewSchema()
	type Inner struct {
	}

	query := schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner := schema.Object("inner", Inner{})
	item := schema.Object("item", Item{})
	item.Key("id")
	inner.PaginateFieldFunc("innerConnection", func(args EmbeddedArgs) ([]Item, schemabuilder.PaginationInfo, error) {
		retList := make([]Item, 5)
		retList[0] = Item{Id: 1}
		retList[1] = Item{Id: 2}
		retList[2] = Item{Id: 3}
		retList[3] = Item{Id: 4}
		retList[4] = Item{Id: 5}
		return retList,
			schemabuilder.PaginationInfo{
				HasNextPage:    true,
				HasPrevPage:    false,
				TotalCountFunc: func() int64 { return int64(5) },
			}, nil
	})
	builtSchema := schema.MustBuild()
	q := graphql.MustParse(`
		{
			inner {
				innerConnection(first: 5, after: "", additional: "jk") {
					totalCount
					edges {
						node {
							id
						}
						cursor
					}
					pageInfo {
						hasNextPage
						hasPrevPage
						startCursor
						endCursor
					}
				}
			}
	    }`, nil)

	if err := graphql.PrepareQuery(builtSchema.Query, q.SelectionSet); err != nil {
		t.Error(err)
	}
	e := graphql.Executor{}
	val, err := e.Execute(context.Background(), builtSchema.Query, nil, q)
	assert.Nil(t, err)

	assert.Equal(t, map[string]interface{}{
		"inner": map[string]interface{}{
			"innerConnection": map[string]interface{}{
				"totalCount": int64(5),
				"edges": []interface{}{
					map[string]interface{}{
						"node": map[string]interface{}{
							"__key": int64(1),
							"id":    int64(1),
						},
						"cursor": "MQ==",
					},
					map[string]interface{}{
						"node": map[string]interface{}{
							"__key": int64(2),
							"id":    int64(2),
						},
						"cursor": "Mg==",
					},
					map[string]interface{}{
						"node": map[string]interface{}{
							"__key": int64(3),
							"id":    int64(3),
						},
						"cursor": "Mw==",
					},
					map[string]interface{}{
						"node": map[string]interface{}{
							"__key": int64(4),
							"id":    int64(4),
						},
						"cursor": "NA==",
					},
					map[string]interface{}{
						"node": map[string]interface{}{
							"__key": int64(5),
							"id":    int64(5),
						},
						"cursor": "NQ==",
					},
				},
				"pageInfo": map[string]interface{}{
					"hasNextPage": true,
					"hasPrevPage": false,
					"startCursor": "MQ==",
					"endCursor":   "NQ==",
				},
			},
		},
	}, val)

	schema = schemabuilder.NewSchema()

	query = schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner = schema.Object("inner", Inner{})
	item = schema.Object("item", Item{})
	item.Key("id")
	inner.PaginateFieldFunc("innerConnection", func(args struct {
		schemabuilder.PaginationArgs
		First string
	}) []Item {
		retList := make([]Item, 5)
		retList[0] = Item{Id: 1}
		retList[1] = Item{Id: 2}
		retList[2] = Item{Id: 3}
		retList[3] = Item{Id: 4}
		retList[4] = Item{Id: 5}
		return retList
	})
	_, err = schema.Build()

	badMethodStr := "bad method inner on type schemabuilder.query:"
	if err == nil || err.Error() != fmt.Sprintf("%v these arg names are restricted: First, After, Last and Before", badMethodStr) {
		t.Errorf("bad error: %v", err)
	}

}

func TestEmbeddedFail(t *testing.T) {
	schema := schemabuilder.NewSchema()
	type Inner struct {
	}

	query := schema.Query()
	query.FieldFunc("inner", func() Inner {
		return Inner{}
	})

	inner := schema.Object("inner", Inner{})
	item := schema.Object("item", Item{})
	item.Key("id")
	inner.PaginateFieldFunc("innerConnection", func(args EmbeddedArgs) ([]Item, schemabuilder.PaginationInfo, error) {
		retList := make([]Item, 5)
		retList[0] = Item{Id: 1}
		retList[1] = Item{Id: 2}
		retList[2] = Item{Id: 3}
		retList[3] = Item{Id: 4}
		retList[4] = Item{Id: 5}
		return retList,
			schemabuilder.PaginationInfo{
				HasNextPage:    true,
				HasPrevPage:    false,
				TotalCountFunc: func() int64 { return int64(5) },
			}, nil
	})

	badMethodStr := "bad method inner on type schemabuilder.query:"
	_, err := schema.Build()
	if err != nil && err.Error() != fmt.Sprintf("%s if pagination args are embedded then pagination info must be included as a return value", badMethodStr) {
		t.Errorf("bad error: %v", err)
	}
}
