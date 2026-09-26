package openapivalidate

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
)

func TestBodySchemaField_OnlyReturnsDefinedPropertyNames(t *testing.T) {
	body := &openapi3.RequestBody{Content: openapi3.Content{
		"application/json": {Schema: &openapi3.SchemaRef{Value: openapi3.NewObjectSchema().
			WithProperty("account", openapi3.NewObjectSchema().WithProperty("password", openapi3.NewStringSchema())).
			WithAdditionalProperties(openapi3.NewStringSchema())}},
	}}

	assert.Equal(t, "password", bodySchemaField(body, []string{"account", "password"}))
	assert.Equal(t, "body", bodySchemaField(body, []string{"user-supplied-secret-key"}))
	assert.Equal(t, "body", bodySchemaField(body, []string{"account", "user-supplied-secret-key"}))
}
