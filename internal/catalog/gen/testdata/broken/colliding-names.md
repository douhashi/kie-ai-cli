# Colliding Names

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  version: 1.0.0
paths:
  /api/v1/jobs/createTask:
    post:
      summary: Colliding Names
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                model:
                  type: string
                  enum:
                    - vendor/colliding-names
                input:
                  type: object
                  properties:
                    'image_urls ':
                      type: array
                    image_urls:
                      type: array
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
