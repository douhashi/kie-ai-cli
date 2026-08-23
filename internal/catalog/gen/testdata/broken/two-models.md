# Two Models

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  version: 1.0.0
paths:
  /api/v1/jobs/createTask:
    post:
      summary: Two Models
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                model:
                  type: string
                  enum:
                    - vendor/first-model
                    - vendor/second-model
                input:
                  type: object
                  properties:
                    prompt:
                      type: string
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
