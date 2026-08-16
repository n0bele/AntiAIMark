// OpenAPI 3.0.3 spec generation — mirrors the Python server's dynamic spec.
package httpapi

func openapiSpec(version, apiKey string) map[string]interface{} {
	booleanSchema := func() map[string]interface{} { return map[string]interface{}{"type": "boolean"} }
	strSchema := func() map[string]interface{} { return map[string]interface{}{"type": "string"} }
	fileRequest := func(extraProps map[string]interface{}, extraRequired []string) map[string]interface{} {
		schema := map[string]interface{}{
			"type":     "object",
			"required": []interface{}{"file"},
			"properties": map[string]interface{}{
				"file": map[string]interface{}{
					"type":        "string",
					"description": "Base64-encoded file bytes",
					"example":     "SGVsbG8gd29ybGQ=",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Original filename (extension drives format routing)",
					"example":     "notes.md",
				},
			},
		}
		props := schema["properties"].(map[string]interface{})
		for k, v := range extraProps {
			props[k] = v
		}
		req := schema["required"].([]interface{})
		for _, r := range extraRequired {
			req = append(req, r)
		}
		return schema
	}
	options := map[string]interface{}{}
	for _, k := range []string{"nfkc", "aggressive_homoglyphs", "keep_non_ai_metadata", "also_layer_a_text", "strip_all_metadata"} {
		options[k] = booleanSchema()
	}
	options["remove_pixel"] = strSchema()

	commonErrors := map[string]interface{}{
		"400": map[string]interface{}{"description": "Bad request", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean", "enum": []interface{}{false}}, "error": strSchema()}}}}},
		"401": map[string]interface{}{"description": "Missing/invalid bearer token", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean", "enum": []interface{}{false}}, "error": strSchema()}}}}},
		"404": map[string]interface{}{"description": "Not found", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean", "enum": []interface{}{false}}, "error": strSchema()}}}}},
		"413": map[string]interface{}{"description": "Request body too large", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean", "enum": []interface{}{false}}, "error": strSchema()}}}}},
		"500": map[string]interface{}{"description": "Internal error", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean", "enum": []interface{}{false}}, "error": strSchema()}}}}},
	}
	op := func(summary string, responses map[string]interface{}, requestBody interface{}) map[string]interface{} {
		m := map[string]interface{}{"summary": summary, "responses": responses}
		if requestBody != nil {
			m["requestBody"] = requestBody
		}
		return m
	}
	success := func(body map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"description": "Success", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": body}}}
	}
	errs := func() map[string]interface{} {
		out := map[string]interface{}{}
		for k, v := range commonErrors {
			out[k] = v
		}
		return out
	}

	paths := map[string]interface{}{
		"/health": map[string]interface{}{
			"get": op("Liveness and version", withErrs(errs(), map[string]interface{}{
				"200": success(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ok": booleanSchema(), "version": strSchema()}}),
			}), nil),
		},
		"/capabilities": map[string]interface{}{
			"get": op("Which optional tools and heavy backends are available", withErrs(errs(), map[string]interface{}{
				"200": success(map[string]interface{}{"type": "object", "properties": map[string]interface{}{
					"ok": booleanSchema(), "version": strSchema(),
					"tools":          map[string]interface{}{"type": "object", "properties": map[string]interface{}{"c2patool": booleanSchema(), "exiftool": booleanSchema(), "qpdf": booleanSchema()}},
					"pixel_backends": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ctrlregen": booleanSchema(), "diffusion": booleanSchema()}},
					"scorers":        map[string]interface{}{"type": "object", "properties": map[string]interface{}{"synthid": booleanSchema()}},
					"harnesses":      map[string]interface{}{"type": "object", "properties": map[string]interface{}{"markllm": booleanSchema()}},
				}}),
			}), nil),
		},
		"/openapi.json": map[string]interface{}{
			"get": op("This OpenAPI 3.0.3 document, generated dynamically", withErrs(errs(), map[string]interface{}{
				"200": success(map[string]interface{}{"type": "object", "description": "An OpenAPI 3.0.3 document"}),
			}), nil),
		},
		"/inspect": map[string]interface{}{
			"post": op("Inspect a file for AI provenance marks (text / image / container auto-routed)", withErrs(errs(), map[string]interface{}{
				"200": success(map[string]interface{}{"type": "object", "properties": map[string]interface{}{
					"ok":         booleanSchema(),
					"kind":       map[string]interface{}{"type": "string", "enum": []interface{}{"text", "image", "container"}},
					"suspicious": booleanSchema(),
					"report":     map[string]interface{}{"type": "object"},
				}}),
			}), map[string]interface{}{
				"required": true,
				"content":  map[string]interface{}{"application/json": map[string]interface{}{"schema": fileRequest(nil, nil)}},
			}),
		},
		"/clean": map[string]interface{}{
			"post": op("Clean a file; returns the cleaned bytes and an actions/stats report", withErrs(errs(), map[string]interface{}{
				"200": success(map[string]interface{}{"type": "object", "properties": map[string]interface{}{
					"ok":      booleanSchema(),
					"kind":    map[string]interface{}{"type": "string", "enum": []interface{}{"text", "image", "container"}},
					"cleaned": map[string]interface{}{"type": "string", "description": "Base64-encoded cleaned file bytes"},
					"report":  map[string]interface{}{"type": "object"},
				}}),
			}), map[string]interface{}{
				"required": true,
				"content": map[string]interface{}{"application/json": map[string]interface{}{"schema": fileRequest(map[string]interface{}{
					"options": map[string]interface{}{"type": "object", "properties": options, "additionalProperties": false},
				}, nil)}},
			}),
		},
	}

	spec := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":   "Watermark Remover service",
			"version": version,
			"description": "Strip multi-vendor AI provenance marks (Unicode, C2PA/EXIF/XMP, containers). " +
				"Files are passed base64-encoded in JSON; cleaned bytes come back base64-encoded.",
		},
		"paths": paths,
	}
	if apiKey != "" {
		spec["components"] = map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]interface{}{"type": "http", "scheme": "bearer"},
			},
		}
		spec["security"] = []interface{}{map[string]interface{}{"bearerAuth": []interface{}{}}}
	}
	return spec
}

func withErrs(errs, ok map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range errs {
		out[k] = v
	}
	for k, v := range ok {
		out[k] = v
	}
	return out
}
