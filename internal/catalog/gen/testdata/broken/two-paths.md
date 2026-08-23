# Two Paths

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  version: 1.0.0
paths:
  /api/v1/jobs/createTask:
    post:
      summary: Create
      responses:
        '200':
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ApiResponse'
  /api/v1/jobs/recordInfo:
    get:
      summary: Query
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
