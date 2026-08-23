# Grok 4.6

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  description: ''
  version: 1.0.0
paths:
  /grok/v1/responses:
    post:
      summary: Grok 4.6
      deprecated: false
      description: >-
        > GPT Grok 4.6 API is a multimodal chat-completions style endpoint that
        accepts structured input arrays, supports adjustable reasoning effort,
        and integrates web search or function calling tools.



        <CardGroup cols={3}>
          <Card title="Multimodal Input" icon="🖼️">
            Supports mixed text, image, and file inputs in a single message.
          </Card>

          <Card title="Reasoning Control" icon="🧠">
            Adjustable reasoning effort from low to xhigh.
          </Card>

          <Card title="Tools & Web Search" icon="✨">
            Integrates web search or custom function calling tools.
          </Card>
        </CardGroup>


        ## Tools & tool\_choice


        The `tools` array enables **web search** or **function calling**
        capabilities.


        :::caution

        Web Search and Function Calling are **mutually exclusive**.  

        In a single request you should choose only one: do not include both
        `{"type": "web_search"}` and `{"type": "function", ...}` in the same
        `tools` array.

        :::



        <AccordionGroup>
          <Accordion title="Web Search">
            Use the built-in Web Search tool to retrieve up-to-date information:

            ```json
            {
              "tools": [
                {
                  "type": "web_search"
                }
              ]
            }
            ```
          </Accordion>

          <Accordion title="Function Calling">
            Define business functions that the model can call when needed:

            ```json
            {
              "tools": [
                {
                  "type": "function",
                  "name": "get_current_weather",
                  "description": "Get the current weather in a given location",
                  "parameters": {
                    "type": "object",
                    "properties": {
                      "location": {
                        "type": "string",
                        "description": "The city and state, e.g. San Francisco, CA"
                      },
                      "unit": {
                        "type": "string",
                        "enum": ["celsius", "fahrenheit"]
                      }
                    },
                    "required": ["location", "unit"]
                  }
                }
              ],
              "tool_choice": "auto"
            }
            ```

            When function tools are configured in `tools`, set `tool_choice` to `"auto"` so the model can decide when to call them.  
            If you do not configure any function tools, omit the `tool_choice` field.
          </Accordion>
        </AccordionGroup>
      operationId: grok/v1/responses
      tags:
        - docs/en/Market/Chat  Models/Grok
      parameters: []
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                model:
                  type: string
                  description: Target model name. Allowed values：`grok-4-6`。
                  examples:
                    - grok-4-6
                stream:
                  type: boolean
                  description: >-
                    When true, responses stream in real time as server-sent
                    events. When false, the full response is returned at once
                    after completion. Default is true.
                  default: true
                input:
                  oneOf:
                    - type: string
                      description: Input can be a string.
                    - type: array
                      description: >-
                        Input can be an array; each element is a message object
                        with role and content.
                      items:
                        type: object
                        properties: {}
                      minItems: 1
                reasoning:
                  type: object
                  description: Reasoning configuration for the model.
                  properties:
                    effort:
                      type: string
                      description: >-
                        Reasoning effort level. Higher values provide more
                        thorough reasoning but may increase latency. Defaults to
                        "low".
                      enum:
                        - low
                        - medium
                        - high
                        - xhigh
                      default: low
                      examples:
                        - low
                      x-apidog-enum:
                        - value: low
                          name: ''
                          description: ''
                        - value: medium
                          name: ''
                          description: ''
                        - value: high
                          name: ''
                          description: ''
                        - value: xhigh
                          name: ''
                          description: ''
                  x-apidog-orders:
                    - effort
                  x-apidog-ignore-properties: []
                tools:
                  type: array
                  description: >-
                    Optional tools that the model may call. Either web search OR
                    function calling should be configured, but not both
                    simultaneously.
                  items:
                    oneOf:
                      - $ref: '#/components/schemas/ToolWebSearch'
                      - $ref: '#/components/schemas/ToolFunction'
                test:
                  $ref: '#/components/schemas/Structured%20outputs'
              required:
                - model
                - input
                - test
              x-apidog-orders:
                - model
                - stream
                - input
                - reasoning
                - tools
                - test
              examples:
                - model: gpt-5.1-codex
                  input:
                    - role: user
                      content:
                        - type: input_text
                          text: What is in this image?
                        - type: input_image
                          image_url: >-
                            https://file.aiquickdraw.com/custom-page/akr/section-images/1759055072437dqlsclj2.png
                  tools:
                    - type: web_search
                  reasoning:
                    effort: high
              x-apidog-ignore-properties: []
            example:
              model: grok-4-6
              stream: true
              input:
                - role: user
                  content:
                    - type: input_text
                      text: Hello, please reply in Chinese
              text:
                format:
                  type: json_schema
                  name: basic_response
                  strict: true
                  schema:
                    type: object
                    properties:
                      answer:
                        type: string
                        description: Response content
                      mood:
                        type: string
                        description: Mood when responding
                    required:
                      - answer
                      - mood
                    additionalProperties: false
      responses:
        '200':
          description: 请求成功。
          content:
            text/event-stream:
              schema:
                type: string
                description: >-
                  流式响应以 Server-Sent Events (SSE) 的形式返回，响应头为 `Content-Type:
                  text/event-stream`。


                  **普通返回**


                  - **文本增量事件**：`event: response.output_text.delta`
                    - `data.delta`：流中的增量文本内容
                    - `data.type`：事件类型，固定为 `response.output_text.delta`
                  - **完成事件**：`event: response.completed`
                    - `data.response.usage`：Token 用量信息，如 `input_tokens`、`output_tokens` 等

                  **函数调用（Function Calling）**


                  - **函数参数增量事件**：`event: response.function_call_arguments.delta`
                    - `data.delta`：函数参数的增量字符串内容
                    - `data.type`：事件类型，固定为 `response.function_call_arguments.delta`
                  - **完成事件**：`event: response.completed`
                    - `data.response.usage`：Token 用量信息，如 `input_tokens`、`output_tokens` 等

                  最后一行 `data: [DONE]` 为流结束标记，表示不会再有新的事件发送。
              example: |-
                {
                  "output": [
                    {
                      "type": "reasoning",
                      "id": "rs_xxx",
                      "summary": []
                    },
                    {
                      "type": "message",
                      "role": "assistant",
                      "id": "msg_xxx",
                      "content": [
                        {
                          "type": "output_text",
                          "text": "Hello! How can I help you today?"
                        }
                      ],
                      "status": "completed"
                    }
                  ],
                  "usage": {
                    "total_tokens": 4490,
                    "output_tokens": 47,
                    "input_tokens": 4443
                  },
                  "credits_consumed": 0.48,
                  "status": "completed"
                }
          headers: {}
          x-apidog-name: ''
        '400':
          description: 错误请求 - 无效的请求参数
          content:
            application/json:
              schema:
                type: object
                properties:
                  error:
                    type: object
                    properties:
                      message:
                        type: string
                        examples:
                          - 无效的请求参数
                      type:
                        type: string
                        examples:
                          - invalid_request_error
                    x-apidog-orders:
                      - message
                      - type
                    x-apidog-ignore-properties: []
                x-apidog-orders:
                  - error
                x-apidog-ignore-properties: []
          headers: {}
          x-apidog-name: ''
        '401':
          description: 未授权 - 无效或缺少 API key
          content:
            application/json:
              schema:
                type: object
                properties:
                  error:
                    type: object
                    properties:
                      message:
                        type: string
                        examples:
                          - 未授权
                      type:
                        type: string
                        examples:
                          - authentication_error
                    x-apidog-orders:
                      - message
                      - type
                    x-apidog-ignore-properties: []
                x-apidog-orders:
                  - error
                x-apidog-ignore-properties: []
          headers: {}
          x-apidog-name: ''
        '429':
          description: 速率限制 - 请求过多
          content:
            application/json:
              schema:
                type: object
                properties:
                  error:
                    type: object
                    properties:
                      message:
                        type: string
                        examples:
                          - 超出速率限制
                      type:
                        type: string
                        examples:
                          - rate_limit_error
                    x-apidog-orders:
                      - message
                      - type
                    x-apidog-ignore-properties: []
                x-apidog-orders:
                  - error
                x-apidog-ignore-properties: []
          headers: {}
          x-apidog-name: ''
        '500':
          description: 请求失败
          content:
            application/json:
              schema:
                type: object
                properties: {}
                x-apidog-orders: []
                x-apidog-ignore-properties: []
          headers: {}
          x-apidog-name: Error
      security:
        - BearerAuth: []
          x-apidog:
            schemeGroups:
              - id: kn8M4YUlc5i0A0179ezwx
                schemeIds:
                  - BearerAuth
            required: true
            use:
              id: kn8M4YUlc5i0A0179ezwx
            scopes:
              kn8M4YUlc5i0A0179ezwx:
                BearerAuth: []
      x-apidog-folder: docs/en/Market/Chat  Models/Grok
      x-apidog-status: released
      x-run-in-apidog: https://app.apidog.com/web/project/1184766/apis/api-41694450-run
components:
  schemas:
    Structured outputs:
      description: 'Text output format configuration '
      type: object
      x-apidog-refs:
        01KX0NZMHJ6JW2A2KRDHDTVR3S:
          $ref: '#/components/schemas/Structured%20outputs1'
          x-apidog-overrides:
            format: &ref_0
              type: object
              properties:
                type:
                  type: string
                  description: ' Format type, fixed value json_schema, indicating the output must conform to specificationJSON Schema'
                name:
                  type: string
                  description: Schema name, used to identify this schema
                strict:
                  type: boolean
                  description: >-
                    Whether to enable strict mode. When true, the output must
                    strictly conform to the schema definition
                schema:
                  type: object
                  properties:
                    type:
                      type: string
                    propertie:
                      type: object
                      properties:
                        answer:
                          type: object
                          properties:
                            type:
                              type: string
                            description:
                              type: string
                              description: 字段描述，如 "回复内容"
                          x-apidog-orders:
                            - type
                            - description
                          required:
                            - type
                            - description
                          x-apidog-ignore-properties: []
                        mood:
                          type: object
                          properties:
                            type:
                              type: string
                            description:
                              type: string
                              description: 字段描述，如 "回复时的心情"
                          x-apidog-orders:
                            - type
                            - description
                          required:
                            - type
                            - description
                          x-apidog-ignore-properties: []
                      x-apidog-orders:
                        - answer
                        - mood
                      description: ' Property definitions, describing which fields are included in the response object'
                      required:
                        - answer
                        - mood
                      x-apidog-ignore-properties: []
                    ' required':
                      type: array
                      items:
                        type: string
                      description: ' List of required fields, e.g. ["answer", "mood"]    '
                    additionalProperties:
                      type: boolean
                      description: ' Whether to allow additional fields not defined in the schema. false means not allowed'
                  x-apidog-orders:
                    - type
                    - propertie
                    - ' required'
                    - additionalProperties
                  description: JSON Schema definition
                  required:
                    - type
                    - propertie
                    - ' required'
                    - additionalProperties
                  x-apidog-ignore-properties: []
              x-apidog-orders:
                - type
                - name
                - strict
                - schema
              required:
                - type
                - name
                - strict
                - schema
              description: Output format definition
              x-apidog-ignore-properties: []
          required:
            - format
      x-apidog-orders:
        - 01KX0NZMHJ6JW2A2KRDHDTVR3S
      properties:
        format: *ref_0
      required:
        - format
      x-apidog-ignore-properties:
        - format
      x-apidog-folder: ''
    Structured outputs1:
      type: object
      properties:
        format:
          type: object
          properties:
            type:
              type: string
              description: 固定值 json_schema
            name:
              type: string
              description: ' Schema 名称，用于标识该 schema'
            strict:
              type: boolean
              description: 是否启用严格模式。为 true 时输出必须严格符合 schema 定义
            schema:
              type: object
              properties:
                type:
                  type: string
                  description: 固定值 object
                propertie:
                  type: object
                  properties:
                    answer:
                      type: object
                      properties:
                        type:
                          type: string
                        description:
                          type: string
                          description: 字段描述，如 "回复内容"
                      x-apidog-orders:
                        - type
                        - description
                      required:
                        - type
                        - description
                      x-apidog-ignore-properties: []
                    mood:
                      type: object
                      properties:
                        type:
                          type: string
                        description:
                          type: string
                          description: 字段描述，如 "回复时的心情"
                      x-apidog-orders:
                        - type
                        - description
                      required:
                        - type
                        - description
                      x-apidog-ignore-properties: []
                  x-apidog-orders:
                    - answer
                    - mood
                  description: 属性定义，描述返回对象中包含哪些字
                  required:
                    - answer
                    - mood
                  x-apidog-ignore-properties: []
                ' required':
                  type: array
                  items:
                    type: string
                  description: '必填字段列表，如 ["answer", "mood"]  '
                additionalProperties:
                  type: boolean
                  description: 是否允许额外字段，false 表示不允许
              x-apidog-orders:
                - type
                - propertie
                - ' required'
                - additionalProperties
              description: ' JSON Schema 定义'
              required:
                - type
                - propertie
                - ' required'
                - additionalProperties
              x-apidog-ignore-properties: []
          x-apidog-orders:
            - type
            - name
            - strict
            - schema
          required:
            - type
            - name
            - strict
            - schema
          description: 输出格式定义
          x-apidog-ignore-properties: []
      x-apidog-orders:
        - format
      required:
        - format
      description: 文本输出格式配置
      x-apidog-ignore-properties: []
      x-apidog-folder: ''
    ToolFunction:
      type: object
      description: 函数调用工具定义。
      properties:
        type:
          type: string
          enum:
            - function
          examples:
            - function
        name:
          type: string
          description: 函数名称。
          examples:
            - get_current_weather
        description:
          type: string
          description: 对该函数用途的可读性描述。
        parameters:
          type: object
          description: 描述函数参数的 JSON Schema。
          x-apidog-orders: []
          properties: {}
          x-apidog-ignore-properties: []
      required:
        - type
        - name
        - description
        - parameters
      x-apidog-orders:
        - type
        - name
        - description
        - parameters
      x-apidog-ignore-properties: []
      x-apidog-folder: ''
    ToolWebSearch:
      type: object
      description: 联网搜索工具配置。
      properties:
        type:
          type: string
          enum:
            - web_search
          examples:
            - web_search
      required:
        - type
      x-apidog-orders:
        - type
      x-apidog-ignore-properties: []
      x-apidog-folder: ''
  securitySchemes:
    BearerAuth:
      type: bearer
      scheme: bearer
      bearerFormat: API Key
      description: >-
        All API requests require a Bearer Token. Add the header `Authorization:
        Bearer YOUR_API_KEY` to authenticate requests.
    BearerAuth1:
      type: bearer
      scheme: bearer
      bearerFormat: API Key
      description: >-
        所有 API 请求都需要 Bearer Token。请在请求头中添加 `Authorization: Bearer YOUR_API_KEY`
        进行身份验证。
servers:
  - url: https://api.kie.ai
    description: 正式环境
security:
  - BearerAuth: []
    x-apidog:
      schemeGroups:
        - id: kn8M4YUlc5i0A0179ezwx
          schemeIds:
            - BearerAuth
      required: true
      use:
        id: kn8M4YUlc5i0A0179ezwx
      scopes:
        kn8M4YUlc5i0A0179ezwx:
          BearerAuth: []

```
