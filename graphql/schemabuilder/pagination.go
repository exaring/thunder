package schemabuilder

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"

	"github.com/samsarahq/thunder/graphql"
)

// Connection conforms to the GraphQL Connection type in the Relay Pagination spec.
type Connection struct {
	TotalCount int64
	Edges      []Edge
	PageInfo   PageInfo
}

// paginateManually applies the pagination arguments to the edges in memory and sets hasNextPage +
// hasPrevPage. The behavior is expected to conform to the Relay Cursor spec:
// https://facebook.github.io/relay/graphql/connections.htm#EdgesToReturn()
func (c *Connection) paginateManually(args PaginationArgs) error {
	var elemsAfter, elemsBefore bool
	c.Edges, elemsAfter, elemsBefore = applyCursorsToAllEdges(c.Edges, args.Before, args.After)

	c.PageInfo.HasNextPage = args.Before != nil && elemsAfter
	c.PageInfo.HasPrevPage = args.After != nil && elemsBefore

	if (safeInt64Ptr(args.First) < 0) || safeInt64Ptr(args.Last) < 0 {
		return graphql.NewClientError("first/last cannot be a negative integer")
	}

	if args.First != nil && args.Last != nil {
		return graphql.NewClientError("Cannot use both first and last together")
	}

	if args.First != nil && len(c.Edges) > int(*args.First) {
		c.Edges = c.Edges[:int(*args.First)]
		c.PageInfo.HasNextPage = true
	}

	if args.Last != nil && len(c.Edges) > int(*args.Last) {
		c.Edges = c.Edges[len(c.Edges)-int(*args.Last):]
		c.PageInfo.HasPrevPage = true
	}
	return nil
}

// setCursors sets the start and end cursors of the current page.
func (c *Connection) setCursors() {
	if len(c.Edges) == 0 {
		return
	}
	c.PageInfo.EndCursor = c.Edges[len(c.Edges)-1].Cursor
	c.PageInfo.StartCursor = c.Edges[0].Cursor
}

// externallySetPageInfo takes in a user-defined PaginationInfo struct,
// using its count, HasNextPage and HasPrevPage information as the source
// of truth.
func (c *Connection) externallySetPageInfo(info PaginationInfo) {
	c.PageInfo.HasNextPage = info.HasNextPage
	c.PageInfo.HasPrevPage = info.HasPrevPage
	c.TotalCount = info.TotalCount()
}

// PageInfo contains information for pagination on a connection type. The list of Pages is used for
// page-number based pagination where the ith index corresponds to the start cursor of (i+1)st page.
type PageInfo struct {
	HasNextPage bool
	EndCursor   string
	HasPrevPage bool
	StartCursor string
	Pages       []string
}

// Edge consists of a node paired with its b64 encoded cursor.
type Edge struct {
	Node   interface{}
	Cursor string
}

// ConnectionArgs conform to the pagination arguments as specified by the Relay Spec for Connection
// types. The Args field consits of the user-facing args.
type ConnectionArgs struct {
	First  *int64
	Last   *int64
	After  *string
	Before *string
	Args   interface{}
}

// PaginationArgs are embedded in a struct
type PaginationArgs struct {
	First  *int64
	Last   *int64
	After  *string
	Before *string
}

func (p PaginationArgs) Limit() int {
	if p.First != nil {
		return int(*p.First)
	}
	if p.Last != nil {
		return int(*p.Last)
	}
	return 0
}

// PaginationInfo can be returned in a PaginateFieldFunc. The TotalCount function returns the
// totalCount field on the connection Type. If the resolver makes a SQL Query, then HasNextPage and
// HasPrevPage can be resolved in an efficient manner by requesting first/last:n + 1 items in the
// query. Then the flags can be filled in by checking the result size.
type PaginationInfo struct {
	TotalCountFunc func() int64
	HasNextPage    bool
	HasPrevPage    bool
}

func (i PaginationInfo) TotalCount() int64 {
	if i.TotalCountFunc == nil {
		return 0
	}
	return i.TotalCountFunc()
}

func getTypeName(typ reflect.Type) string {
	if typ.Kind() == reflect.Ptr {
		return typ.Elem().Name()
	}
	return fmt.Sprintf("NonNull%s", typ.Name())
}

type connectionContext struct {
	*funcContext
	// The string value for the key field name.
	Key string
	// Whether or not the FieldFunc returns PageInfo (overrides thunder's auto-populated PageInfo).
	ReturnsPageInfo bool
	// The index of PaginationArgs in the arguments provided to the FieldFunc.
	PaginationArgsIndex int
}

// embedsPaginationArgs returns true if PaginationArgs were embedded.
func (c *connectionContext) embedsPaginationArgs() bool {
	return c.PaginationArgsIndex != -1
}

// IsExternallyManaged returns true if the connection is managed by the FieldFunc's function
// and not thunder.
func (c *connectionContext) IsExternallyManaged() bool {
	return c.embedsPaginationArgs() || c.ReturnsPageInfo
}

// Validate returns an error if the connection isn't correctly implemented.
func (c *connectionContext) Validate() error {
	if c.IsExternallyManaged() && !(c.embedsPaginationArgs() && c.ReturnsPageInfo) {
		return fmt.Errorf("If pagination args are embedded then pagination info must be included as a return value")
	}
	return nil
}

// constructEdgeType wraps the typ (which is the type of the Node) in an Edge type conforming to the
// Relay spec.
func (sb *schemaBuilder) constructEdgeType(typ reflect.Type) (graphql.Type, error) {
	nodeType, err := sb.getType(typ)
	if err != nil {
		return nil, err
	}

	fieldMap := make(map[string]*graphql.Field)

	nodeField := &graphql.Field{
		Resolve: func(ctx context.Context, source, args interface{}, selectionSet *graphql.SelectionSet) (interface{}, error) {
			if value, ok := source.(Edge); ok {
				return value.Node, nil
			}

			return nil, fmt.Errorf("error resolving node in edge")

		},
		Type:           nodeType,
		ParseArguments: nilParseArguments,
	}
	fieldMap["node"] = nodeField

	cursorType, err := sb.getType(reflect.TypeOf(string("")))
	if err != nil {
		return nil, err
	}

	cursorField := &graphql.Field{
		Resolve: func(ctx context.Context, source, args interface{}, selectionSet *graphql.SelectionSet) (interface{}, error) {
			if value, ok := source.(Edge); ok {
				return value.Cursor, nil
			}
			return nil, fmt.Errorf("error resolving cursor in edge")
		},
		Type:           cursorType,
		ParseArguments: nilParseArguments,
	}

	fieldMap["cursor"] = cursorField

	return &graphql.NonNull{
		Type: &graphql.Object{
			Name:        fmt.Sprintf("%sEdge", getTypeName(typ)),
			Description: "",
			Fields:      fieldMap,
		},
	}, nil

}

// constructConnType wraps typ (type of the Node) in a Connection Type conforming to the Relay spec.
func (c *connectionContext) constructConnType(sb *schemaBuilder, typ reflect.Type) (graphql.Type, error) {
	fieldMap := make(map[string]*graphql.Field)

	countType, _ := reflect.TypeOf(Connection{}).FieldByName("TotalCount")
	countField, err := sb.buildField(countType)
	if err != nil {
		return nil, err
	}

	fieldMap["totalCount"] = countField
	edgeType, err := sb.constructEdgeType(typ)
	if err != nil {
		return nil, err
	}

	edgesSliceType := &graphql.NonNull{Type: &graphql.List{Type: edgeType}}

	edgesSliceField := &graphql.Field{
		Resolve: func(ctx context.Context, source, args interface{}, selectionSet *graphql.SelectionSet) (interface{}, error) {
			if value, ok := source.(Connection); ok {
				return value.Edges, nil
			}
			return nil, fmt.Errorf("error resolving edges in connection")
		},
		Type:           edgesSliceType,
		ParseArguments: nilParseArguments,
	}

	fieldMap["edges"] = edgesSliceField

	pageInfoType, _ := reflect.TypeOf(Connection{}).FieldByName("PageInfo")
	pageInfoField, err := sb.buildField(pageInfoType)
	pageInfoNonNull, _ := pageInfoField.Type.(*graphql.NonNull)
	pageInfoObj := pageInfoNonNull.Type.(*graphql.Object)

	// If a PaginateFieldFunc returns connection info then it means that the resolver needs to
	// handle slicing according to the connection args. Hence, it's no longer feasible to determine
	// the entire set of pages on the connection.
	if c.IsExternallyManaged() {
		delete(pageInfoObj.Fields, "pages")
	}
	if err != nil {
		return nil, err
	}
	fieldMap["pageInfo"] = pageInfoField
	retObject := &graphql.NonNull{
		Type: &graphql.Object{
			Name:        fmt.Sprintf("%sConnection", getTypeName(typ)),
			Description: "",
			Fields:      fieldMap,
		},
	}
	return retObject, nil
}

func safeInt64Ptr(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}

// getCursorIndex returns the index corresponding to the cursor in the slice.
func getCursorIndex(edges []Edge, cursor string) int {
	for i, val := range edges {
		if val.Cursor == cursor {
			return i
		}
	}
	return -1
}

// applyCursorsToAllEdges returns the slice of edges after applying the after and before arguments.
// It also implements part of the hasNextPage and hasPrevPage algorithm by returning if there are
// elements after or before the arguments.
func applyCursorsToAllEdges(edges []Edge, before *string, after *string) ([]Edge, bool, bool) {
	edgeCount := len(edges)
	elemsAfter := false
	elemsBefore := false

	if after != nil {
		i := getCursorIndex(edges, *after)
		if i != -1 {
			edges = edges[i+1:]
			if i != 0 {
				elemsBefore = true
			}
		}

	}
	if before != nil {
		i := getCursorIndex(edges, *before)
		if i != -1 {
			edges = edges[:i]
			if i != edgeCount-1 {
				elemsAfter = true
			}
		}

	}

	return edges, elemsAfter, elemsBefore

}

func getEdges(key string, nodes []interface{}) (edges []Edge) {
	for _, node := range nodes {
		keyValue := reflect.ValueOf(node)
		if keyValue.Kind() == reflect.Ptr {
			keyValue = keyValue.Elem()
		}
		keyString := []byte(fmt.Sprintf("%v", keyValue.FieldByName(key).Interface()))
		cursorVal := base64.StdEncoding.EncodeToString(keyString)
		edges = append(edges, Edge{Node: node, Cursor: cursorVal})
	}

	return edges
}

// Creates a pages slice, starting with a blank cursor, then every n+1 edge's cursor (if you have 20
// entries per page, 19, 39, 59 etc). This works for `after:` but works unexpectedly for `before:`

// NOTE: The cursors are based off of the total and are not relative to the current query, meaning
// that they will shift with each query as entries are added.
func getPages(edges []Edge, limit int) (pages []string) {
	for i, edge := range edges {
		// The blank cursor indicates the initial page.
		if i == 0 {
			pages = append(pages, "")
		}

		// Limit at zero means infinite / no pages.
		if limit == 0 {
			continue
		}
		// The last cursor can't be followed by another page because there are no more entries.
		if i == len(edges)-1 {
			continue
		}
		// If the next cursor is the start cursor of a page then push the current cursor
		// to the list.
		if (i+1)%limit == 0 {
			pages = append(pages, edge.Cursor)
		}
	}

	return pages
}

// getConnection applies the ConnectionArgs to nodes and returns the result in a wrapped Connection
// type.
func (c *connectionContext) getConnection(out []reflect.Value, args PaginationArgs) (Connection, error) {
	nodes := castSlice(out[0].Interface())
	if len(nodes) == 0 {
		return Connection{}, nil
	}

	limit := args.Limit()
	edges := getEdges(c.Key, nodes)
	pages := getPages(edges, limit)
	connection := Connection{
		TotalCount: int64(len(nodes)),
		Edges:      edges,
		PageInfo: PageInfo{
			Pages: pages,
		},
	}
	if err := connection.paginateManually(args); err != nil {
		return Connection{}, err
	}
	connection.setCursors()

	if c.IsExternallyManaged() {
		connection.externallySetPageInfo(out[1].Interface().(PaginationInfo))
	}
	return connection, nil

}

// PaginateFieldFunc registers a function that is also paginated according to the Relay
// Connection Spec. The field is registered as a Connection Type and first, last, before and after
// are automatically added as arguments to the function. The return type to the function must be a
// list. The element of the list is wrapped as a Node Type.
// If the resolver needs to use the pagination arguments, then the PaginationArgs struct must be
// embedded in the args struct passed in the resolver function, and the PaginationInfo struct needs
// to be returned in the resolver func.
//
// Deprecated: Use FieldFunc(name, func, Paginated) instead.
func (o *Object) PaginateFieldFunc(name string, f interface{}) {
	o.FieldFunc(name, f, Paginated)
}

// indexOfPaginationArgs gets the index of PaginationArgs if they were embedded in a struct,
// otherwise returns -1.
func indexOfPaginationArgs(argType reflect.Type) int {
	for i := 0; i < argType.NumField(); i++ {
		field := argType.Field(i)

		if field.Type == reflect.TypeOf(PaginationArgs{}) {
			return i
		}
	}
	return -1
}

func (c *connectionContext) consumePaginatedArgs(sb *schemaBuilder, in []reflect.Type) (*argParser, graphql.Type, []reflect.Type, error) {
	var argParser *argParser
	var argType graphql.Type
	var err error
	c.PaginationArgsIndex = -1
	// If the args passed into paginated field func embed the PaginationArgs then the arg parser
	// needs to be constructed differently from the default case.
	if len(in) > 0 && in[0] != selectionSetType {
		c.PaginationArgsIndex = indexOfPaginationArgs(in[0])
		if c.IsExternallyManaged() {
			argParser, argType, err = sb.buildEmbeddedPaginatedArgParser(in[0])
			if err != nil {
				return nil, nil, in, err
			}
		} else {
			argParser, argType, err = sb.buildPaginatedArgParser(in[0])
			if err != nil {
				return nil, nil, in, err
			}
		}
		in = in[1:]
	} else {
		argParser, argType, err = sb.buildPaginatedArgParser(nil)
		if err != nil {
			return nil, nil, in, err
		}

	}

	return argParser, argType, in, nil
}

func (sb *schemaBuilder) getKeyFieldOnStruct(nodeType reflect.Type) (string, error) {
	nodeObj := sb.objects[nodeType]
	if nodeObj == nil && nodeType.Kind() == reflect.Ptr {
		nodeObj = sb.objects[nodeType.Elem()]
	}
	if nodeObj == nil {
		return "", fmt.Errorf("%s must be a struct and registered as an object along with its key", nodeType)
	}
	nodeKey := reverseGraphqlFieldName(nodeObj.key)
	if nodeKey == "" {
		return nodeKey, fmt.Errorf("a key field must be registered for paginated objects")
	}
	if nodeType.Kind() == reflect.Ptr {
		nodeType = nodeType.Elem()
	}
	if _, ok := nodeType.FieldByName(nodeKey); !ok {
		return nodeKey, fmt.Errorf("field doesn't exist on struct")
	}

	return nodeKey, nil

}

// Parses the return types and checks if there's a pageInfo struct being returned by the resolver
func (c *connectionContext) parsePaginatedReturnSignature(m *method) (err error) {
	c.ReturnsPageInfo = false

	out := make([]reflect.Type, 0, c.funcType.NumOut())
	for i := 0; i < c.funcType.NumOut(); i++ {
		out = append(out, c.funcType.Out(i))
	}

	if len(out) > 0 && out[0] != errType {
		c.hasRet = true
		out = out[1:]
	}

	if len(out) > 0 && out[0] == reflect.TypeOf(PaginationInfo{}) {
		c.ReturnsPageInfo = true
		out = out[1:]
	}

	if len(out) > 0 && out[0] == errType {
		c.hasError = true
		out = out[1:]
	}
	if len(out) != 0 {
		err = fmt.Errorf("%s return values should [result][, error]", c.funcType)
		return
	}

	if !c.hasRet && m.MarkedNonNullable {
		err = fmt.Errorf("%s is marked non-nullable, but has no return value", c.funcType)
		return
	}
	return

}

// buildPaginatedField corresponds to buildFunction on a paginated type. It wraps the return result
// of f in a connection type.
func (sb *schemaBuilder) buildPaginatedField(typ reflect.Type, m *method) (*graphql.Field, error) {
	c := &connectionContext{funcContext: &funcContext{typ: typ}}

	fun, err := c.getFuncVal(m)
	if err != nil {
		return nil, err
	}

	in := c.getFuncInputTypes()
	in = c.consumeContextAndSource(in)

	argParser, argType, in, err := c.consumePaginatedArgs(sb, in)
	if err != nil {
		return nil, err
	}
	c.hasArgs = true

	in = c.consumeSelectionSet(in)

	// We have succeeded if no arguments remain.
	if len(in) != 0 {
		return nil, fmt.Errorf("%s arguments should be [context][, [*]%s][, args][, selectionSet]", c.funcType, typ)
	}

	// Parse return values. The first return value must be the actual value, and
	// the second value can optionally be an error.
	if err := c.parsePaginatedReturnSignature(&method{MarkedNonNullable: true}); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}

	// It's safe to assume that there's a return type since the method is marked as non-nullable
	// when calling parseReturnSignature above.
	if c.funcType.Out(0).Kind() != reflect.Slice {
		return nil, fmt.Errorf("paginated field func must return a slice type")
	}
	nodeType := c.funcType.Out(0).Elem()
	retType, err := c.constructConnType(sb, nodeType)
	if err != nil {
		return nil, err
	}

	c.Key, err = sb.getKeyFieldOnStruct(nodeType)
	if err != nil {
		return nil, err
	}

	args, err := c.argsTypeMap(argType)

	ret := &graphql.Field{
		Resolve: func(ctx context.Context, source, args interface{}, selectionSet *graphql.SelectionSet) (interface{}, error) {
			argsVal := args
			if !c.IsExternallyManaged() {
				val, ok := args.(ConnectionArgs)
				if !ok {
					return nil, fmt.Errorf("arguments should implement ConnectionArgs")
				}
				c.hasArgs = val.Args != nil
				if c.hasArgs {
					argsVal = reflect.ValueOf(val.Args).Elem().Interface()
				}
			}

			in := c.prepareResolveArgs(source, argsVal, ctx)

			// Call the function.
			out := fun.Call(in)

			return c.extractPaginatedRetAndErr(out, args, retType)

		},
		Args:           args,
		Type:           retType,
		ParseArguments: argParser.Parse,
		Expensive:      c.hasContext,
	}

	return ret, nil
}

func (c *connectionContext) extractPaginatedRetAndErr(out []reflect.Value, args interface{}, retType graphql.Type) (interface{}, error) {
	var paginationArgs PaginationArgs

	// If the pagination args are not embedded then they need to be extracted out of ConnectionArgs
	// struct and setup for the slicing functions.
	if !c.IsExternallyManaged() {
		connectionArgs, _ := args.(ConnectionArgs)
		paginationArgs = PaginationArgs{
			First:  connectionArgs.First,
			Last:   connectionArgs.Last,
			After:  connectionArgs.After,
			Before: connectionArgs.Before,
		}
	} else {
		paginationArgs = reflect.ValueOf(args).Field(c.PaginationArgsIndex).Interface().(PaginationArgs)
	}

	result, err := c.getConnection(out, paginationArgs)
	if err != nil {
		return nil, err
	}
	if c.hasError {
		if err := out[len(out)-1]; !err.IsNil() {
			return nil, err.Interface().(error)
		}
	}

	return result, nil
}

func castSlice(slice interface{}) []interface{} {
	s := reflect.ValueOf(slice)
	if s.Kind() != reflect.Slice {
		panic("cast given a non-slice type")
	}

	ret := make([]interface{}, s.Len())
	for i := 0; i < s.Len(); i++ {
		ret[i] = s.Index(i).Interface()
	}

	return ret
}

// buildEmbeddedArgParser when the user embeds in the pagination args.
func (sb *schemaBuilder) buildEmbeddedPaginatedArgParser(typ reflect.Type) (*argParser, graphql.Type, error) {
	fields := make(map[string]argField)

	argType := &graphql.InputObject{
		Name:        typ.Name(),
		InputFields: make(map[string]graphql.Type),
	}
	pagArgIndex := 0
	argType.Name += "_InputObject"
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		// The field which is of type interface should only be one and will be used to parse the
		// original function args.
		if field.Type.Kind() == reflect.Interface {
			continue
		}
		if field.Type == reflect.TypeOf(PaginationArgs{}) {
			pagArgIndex = i
			continue
		}

		name := makeGraphql(field.Name)

		var parser *argParser
		var fieldArgTyp graphql.Type

		parser, fieldArgTyp, err := sb.makeArgParser(field.Type)
		if err != nil {
			return nil, nil, err
		}

		argType.InputFields[name] = fieldArgTyp
		fields[name] = argField{
			field:  field,
			parser: parser,
		}
	}

	pagArgParser, pagArgType, err := sb.makeStructParser(reflect.TypeOf(PaginationArgs{}))
	if err != nil {
		return nil, nil, err
	}
	pagObj, ok := pagArgType.(*graphql.InputObject)
	if !ok {
		panic("failed to cast paginated args to an input object")
	}
	for name, objField := range pagObj.InputFields {
		if _, ok := argType.InputFields[name]; ok {
			return nil, nil, fmt.Errorf("these arg names are restricted: First, After, Last and Before")
		}
		argType.InputFields[name] = objField
	}
	return &argParser{
		FromJSON: func(value interface{}, dest reflect.Value) error {
			asMap, ok := value.(map[string]interface{})
			if !ok {
				return errors.New("not an object")
			}

			for name, field := range fields {
				value := asMap[name]
				fieldDest := dest.FieldByIndex(field.field.Index)
				if err := field.parser.FromJSON(value, fieldDest); err != nil {
					return fmt.Errorf("%s: %s", name, err)
				}
			}

			// nestedArgFields is the map used to parse the remaining fields: any field which isn't
			// part of ConnectionArgs should be a field of the args used for the paginated field.
			pagArgFields := make(map[string]interface{})
			for name := range asMap {
				if _, ok := fields[name]; !ok {
					pagArgFields[name] = asMap[name]
				}
			}

			fieldDest := dest.Field(pagArgIndex)
			if err := pagArgParser.FromJSON(pagArgFields, fieldDest); err != nil {
				return err
			}

			return nil
		},
		Type: typ,
	}, argType, nil

}

// buildPaginatedArgParser corresponds to buildArgParser for arguments used on a paginated
// fieldFunc. The args are nested as the Args field in the ConnectionArgs.
func (sb *schemaBuilder) buildPaginatedArgParser(originalArgType reflect.Type) (*argParser, graphql.Type, error) {
	//nestedArgParser and nestedArgType are used for building the parser function for the args
	//passed in to the paginated field.
	typ := reflect.TypeOf(ConnectionArgs{})

	// Fields build a map of the fields for ConnectionArgs.
	fields := make(map[string]argField)

	argType := &graphql.InputObject{
		Name:        typ.Name(),
		InputFields: make(map[string]graphql.Type),
	}

	argType.Name += "_InputObject"

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		// The field which is of type interface should only be one and will be used to parse the
		// original function args.
		if field.Type.Kind() == reflect.Interface {
			continue
		}

		name := makeGraphql(field.Name)

		var parser *argParser
		var fieldArgTyp graphql.Type

		parser, fieldArgTyp, err := sb.makeArgParser(field.Type)
		if err != nil {
			return nil, nil, err
		}

		argType.InputFields[name] = fieldArgTyp

		fields[name] = argField{
			field:  field,
			parser: parser,
		}
	}

	var nestedArgParser *argParser
	var nestedArgType graphql.Type
	var err error
	if originalArgType != nil {
		nestedArgParser, nestedArgType, err = sb.makeStructParser(originalArgType)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build args for paginated field")
		}
		userInputObject, ok := nestedArgType.(*graphql.InputObject)
		if !ok {
			return nil, nil, fmt.Errorf("args should be an object")
		}

		for name, typ := range userInputObject.InputFields {
			argType.InputFields[name] = typ
		}
	}

	return &argParser{
		FromJSON: func(value interface{}, dest reflect.Value) error {
			asMap, ok := value.(map[string]interface{})
			if !ok {
				return errors.New("not an object")
			}

			for name, field := range fields {
				value := asMap[name]
				fieldDest := dest.FieldByIndex(field.field.Index)
				if err := field.parser.FromJSON(value, fieldDest); err != nil {
					return fmt.Errorf("%s: %s", name, err)
				}
			}

			// nestedArgFields is the map used to parse the remaining fields: any field which isn't
			// part of ConnectionArgs should be a field of the args used for the paginated field.
			nestedArgFields := make(map[string]interface{})
			for name := range asMap {
				if _, ok := fields[name]; !ok {
					nestedArgFields[name] = asMap[name]
				}
			}

			if nestedArgParser == nil {
				if len(nestedArgFields) != 0 {
					return fmt.Errorf("error in parsing args")
				}
				return nil
			}

			fieldDest := dest.FieldByName("Args")
			tmpDest := reflect.New(nestedArgParser.Type)
			if err := nestedArgParser.FromJSON(nestedArgFields, tmpDest.Elem()); err != nil {
				return err
			}
			fieldDest.Set(tmpDest)

			return nil
		},
		Type: typ,
	}, argType, nil
}
