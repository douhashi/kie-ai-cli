# Not Market

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  version: 1.0.0
paths:
  /api/v1/generate:
    post:
      summary: Not Market
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                prompt:
                  type: string
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
