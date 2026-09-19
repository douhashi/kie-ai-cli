# Spaced Names

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  version: 1.0.0
paths:
  /api/v1/jobs/createTask:
    post:
      summary: Spaced Names
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                model:
                  type: string
                  enum:
                    - vendor/spaced-names
                input:
                  type: object
                  properties:
                    'image_urls ':
                      type: array
                      items:
                        type: string
                    prompt:
                      type: string
                  required:
                    - 'image_urls '
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  data:
                    type: object
                    properties:
                      taskId:
                        type: string
```
