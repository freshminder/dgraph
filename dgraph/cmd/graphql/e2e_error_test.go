/*
 * Copyright 2019 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package graphql

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http/httptest"
	"testing"

	dgoapi "github.com/dgraph-io/dgo/v2/protos/api"
	"github.com/dgraph-io/dgraph/dgraph/cmd/graphql/resolve"
	"github.com/dgraph-io/dgraph/dgraph/cmd/graphql/test"
	"github.com/dgraph-io/dgraph/dgraph/cmd/graphql/web"
	"github.com/dgraph-io/dgraph/gql"
	"github.com/dgraph-io/dgraph/x"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

type ErrorCase struct {
	Name       string
	GQLRequest string
	variables  map[string]interface{}
	Errors     x.GqlErrorList
}

// TestRequestValidationErrors just makes sure we are catching validation failures.
// Mostly this is provided by an external lib, so just checking we hit common cases.
func TestRequestValidationErrors(t *testing.T) {
	b, err := ioutil.ReadFile("e2e_error_test.yaml")
	require.NoError(t, err, "Unable to read test file")

	var tests []ErrorCase
	err = yaml.Unmarshal(b, &tests)
	require.NoError(t, err, "Unable to unmarshal test cases from yaml.")

	for _, tcase := range tests {
		t.Run(tcase.Name, func(t *testing.T) {
			test := &GraphQLParams{
				Query:     tcase.GQLRequest,
				Variables: tcase.variables,
			}
			gqlResponse := test.ExecuteAsPost(t, graphqlURL)

			require.Nil(t, gqlResponse.Data)
			if diff := cmp.Diff(tcase.Errors, gqlResponse.Errors); diff != "" {
				t.Errorf("errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPanicCatcher tests that the GraphQL server behaves properly when an internal
// bug triggers a panic.  Here, this is mocked up with httptest and a dgraph package
// that just panics.
//
// Not really an e2e test cause it uses httptest and mocks up a panicing Dgraph, but
// uses all the e2e infrastructure.
func TestPanicCatcher(t *testing.T) {

	// queries and mutations have different panic paths.
	//
	// Because queries run concurrently in their own goroutine, any panics are
	// caught by a panic handler deferred when starting those goroutines.
	//
	// Mutations run serially in the same goroutine as the original http handler,
	// so a panic here is caught by the panic catching http handler that wraps
	// the http stack.

	tests := map[string]*GraphQLParams{
		"query": &GraphQLParams{Query: `query { queryCountry { name } }`},
		"mutation": &GraphQLParams{
			Query: `mutation {
						addCountry(input: { name: "A Country" }) { country { id } }
					}`,
		},
	}

	gqlSchema := test.LoadSchemaFromFile(t, "e2e_test_schema.graphql")

	fns := &resolve.ResolverFns{
		Qrw: resolve.NewQueryRewriter(),
		Mrw: resolve.NewMutationRewriter(),
		Drw: resolve.NewDeleteRewriter(),
		Qe:  &panicClient{},
		Me:  &panicClient{}}

	resolverFactory := resolve.NewResolverFactory().
		WithConventionResolvers(gqlSchema, fns)

	resolvers := resolve.New(gqlSchema, resolverFactory)
	server := web.NewServer(resolvers)

	ts := httptest.NewServer(server.HTTPHandler())
	defer ts.Close()

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			gqlResponse := test.ExecuteAsPost(t, ts.URL)

			require.Equal(t, x.GqlErrorList{
				{Message: fmt.Sprintf("[%s] Internal Server Error - a panic was trapped.  "+
					"This indicates a bug in the GraphQL server.  A stack trace was logged.  "+
					"Please let us know : https://github.com/dgraph-io/dgraph/issues.",
					gqlResponse.Extensions["requestID"].(string))}},
				gqlResponse.Errors)

			require.Nil(t, gqlResponse.Data)
		})
	}
}

type panicClient struct{}

func (dg *panicClient) Query(ctx context.Context, query *gql.GraphQuery) ([]byte, error) {
	panic("bugz!!!")
}

func (dg *panicClient) Mutate(
	ctx context.Context,
	query *gql.GraphQuery,
	mutations []*dgoapi.Mutation) (map[string]string, map[string][]string, error) {
	panic("bugz!!!")
}
