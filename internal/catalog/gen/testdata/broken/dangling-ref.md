# Dangling Ref

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  version: 1.0.0
paths:
  /api/v1/jobs/createTask:
    post:
      summary: Dangling Ref
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                model:
                  type: string
                  enum:
                    - vendor/dangling-ref
                input:
                  type: object
                  properties: {}
                  x-apidog-refs:
                    01KWKKAYD4AP9MQ0XGF3QAJP22:
                      $ref: '#/components/schemas/nsfw_checker'
      responses:
        '200':
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ApiResponse'
components:
  schemas:
    ApiResponse:
      type: object
      properties:
        data:
          type: object
          properties:
            taskId:
              type: string
```
