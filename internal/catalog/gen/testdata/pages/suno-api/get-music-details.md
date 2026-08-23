# Get Music Task Details

## OpenAPI Specification

```yaml
openapi: 3.0.1
info:
  title: ''
  description: ''
  version: 1.0.0
paths:
  /api/v1/generate/record-info:
    get:
      summary: Get Music Task Details
      deprecated: false
      description: >-
        Retrieve detailed information about a music generation task.


        ### Usage Guide

        - Use this endpoint to check task status and access generation results

        - Task details include status, parameters, and generated tracks

        - Generated tracks can be accessed through the returned URLs


        ### Status Descriptions

        - `PENDING`: Task is waiting to be processed

        - `TEXT_SUCCESS`: Lyrics/text generation completed successfully

        - `FIRST_SUCCESS`: First track generation completed

        - `SUCCESS`: All tracks generated successfully

        - `CREATE_TASK_FAILED`: Failed to create task

        - `GENERATE_AUDIO_FAILED`: Failed to generate audio

        - `CALLBACK_EXCEPTION`: Error during callback process

        - `SENSITIVE_WORD_ERROR`: Content filtered due to sensitive words


        ### Developer Notes

        - For instrumental tracks (`instrumental=true`), no lyrics data will be
        included

        - Maximum query rate: 3 requests per second per task

        - Response includes direct URLs to audio files, images, and streaming
        endpoints
      operationId: get-music-details
      tags:
        - docs/en/Market/Suno API/Music Generation
      parameters:
        - name: taskId
          in: query
          description: >-
            Unique identifier of the music generation task to retrieve. This can
            be either a taskId from a "Generate Music" task or an "Extend Music"
            task.
          required: true
          example: 5c79****be8e
          schema:
            type: string
      responses:
        '200':
          description: Request successful
          content:
            application/json:
              schema:
                allOf:
                  - type: object
                    properties:
                      code:
                        type: integer
                        enum:
                          - 200
                          - 401
                          - 404
                          - 422
                          - 451
                          - 455
                          - 500
                        description: >-
                          Response status code


                          - **200**: Success - Request has been processed
                          successfully

                          - **401**: Unauthorized - Authentication credentials
                          are missing or invalid

                          - **404**: Not Found - The requested resource or
                          endpoint does not exist

                          - **422**: Validation Error - The request parameters
                          failed validation checks

                          - **451**: Unauthorized - Failed to fetch the image.
                          Kindly verify any access limits set by you or your
                          service provider.

                          - **455**: Service Unavailable - System is currently
                          undergoing maintenance

                          - **500**: Server Error - An unexpected error occurred
                          while processing the request
                      msg:
                        type: string
                        description: Error message when code != 200
                        examples:
                          - success
                    x-apidog-orders:
                      - code
                      - msg
                    x-apidog-ignore-properties: []
                  - type: object
                    properties:
                      data:
                        type: object
                        properties:
                          taskId:
                            type: string
                            description: Task ID
                          parentMusicId:
                            type: string
                            description: Parent music ID (only valid when extending music)
                          param:
                            type: string
                            description: Parameter information for task generation
                          response:
                            type: object
                            properties:
                              taskId:
                                type: string
                                description: Task ID
                              sunoData:
                                type: array
                                items:
                                  type: object
                                  properties:
                                    id:
                                      type: string
                                      description: Audio unique identifier (audioId)
                                    audioUrl:
                                      type: string
                                      description: Audio file URL
                                    streamAudioUrl:
                                      type: string
                                      description: Streaming audio URL
                                    imageUrl:
                                      type: string
                                      description: Cover image URL
                                    prompt:
                                      type: string
                                      description: Generation prompt/lyrics
                                    modelName:
                                      type: string
                                      description: Model name used
                                    title:
                                      type: string
                                      description: Music title
                                    tags:
                                      type: string
                                      description: Music tags
                                    createTime:
                                      type: string
                                      description: Creation time
                                      format: date-time
                                    duration:
                                      type: number
                                      description: Audio duration (seconds)
                                  x-apidog-orders:
                                    - id
                                    - audioUrl
                                    - streamAudioUrl
                                    - imageUrl
                                    - prompt
                                    - modelName
                                    - title
                                    - tags
                                    - createTime
                                    - duration
                                  x-apidog-ignore-properties: []
                            x-apidog-orders:
                              - taskId
                              - sunoData
                            x-apidog-ignore-properties: []
                          status:
                            type: string
                            description: Task status
                            enum:
                              - PENDING
                              - TEXT_SUCCESS
                              - FIRST_SUCCESS
                              - SUCCESS
                              - CREATE_TASK_FAILED
                              - GENERATE_AUDIO_FAILED
                              - CALLBACK_EXCEPTION
                              - SENSITIVE_WORD_ERROR
                          type:
                            type: string
                            enum:
                              - chirp-v3-5
                              - chirp-v4
                            description: Task type
                          operationType:
                            type: string
                            enum:
                              - generate
                              - extend
                              - upload_cover
                              - upload_extend
                            description: >-
                              Operation Type


                              - `generate`: Generate Music - Create new music
                              works using AI model

                              - `extend`: Extend Music - Extend or modify
                              existing music works

                              - `upload_cover`: Upload And Cover Audio - Create
                              new music works based on uploaded audio files

                              - `upload_extend`: Upload And Extend Audio -
                              Extend or modify music works based on uploaded
                              audio files
                          errorCode:
                            type: integer
                            format: int32
                            description: >-
                              Error code


                              - **400**: Validation Error - Lyrics contained
                              copyrighted material.

                              - **408**: Rate Limited - Timeout.

                              - **413**: Conflict - Uploaded audio matches
                              existing work of art.
                            enum:
                              - 400
                              - 408
                              - 413
                          errorMessage:
                            type: string
                            description: Error message
                            examples:
                              - ''
                        x-apidog-orders:
                          - taskId
                          - parentMusicId
                          - param
                          - response
                          - status
                          - type
                          - operationType
                          - errorCode
                          - errorMessage
                        x-apidog-ignore-properties: []
                    x-apidog-orders:
                      - data
                    x-apidog-ignore-properties: []
              example:
                code: 200
                msg: success
                data:
                  taskId: 5c79****be8e
                  parentMusicId: ''
                  param: >-
                    {"prompt":"A calm piano
                    track","style":"Classical","title":"Peaceful
                    Piano","customMode":true,"instrumental":true,"model":"V3_5"}
                  response:
                    taskId: 5c79****be8e
                    sunoData:
                      - id: e231****-****-****-****-****8cadc7dc
                        audioUrl: https://example.cn/****.mp3
                        streamAudioUrl: https://example.cn/****
                        imageUrl: https://example.cn/****.jpeg
                        prompt: '[Verse] 夜晚城市 灯火辉煌'
                        modelName: chirp-v3-5
                        title: 钢铁侠
                        tags: electrifying, rock
                        createTime: '2025-01-01 00:00:00'
                        duration: 198.44
                  status: SUCCESS
                  type: GENERATE
                  errorCode: null
                  errorMessage: null
          headers: {}
          x-apidog-name: ''
        '500':
          description: request failed
          content:
            application/json:
              schema:
                type: object
                properties:
                  code:
                    type: integer
                    description: >-
                      Response status code


                      - **200**: Success - Request has been processed
                      successfully

                      - **401**: Unauthorized - Authentication credentials are
                      missing or invalid

                      - **402**: Insufficient Credits - Account does not have
                      enough credits to perform the operation

                      - **404**: Not Found - The requested resource or endpoint
                      does not exist

                      - **408**: Upstream is currently experiencing service
                      issues. No result has been returned for over 10 minutes.

                      - **422**: Validation Error - The request parameters
                      failed validation checks

                      - **429**: Rate Limited - Request limit has been exceeded
                      for this resource

                      - **455**: Service Unavailable - System is currently
                      undergoing maintenance

                      - **500**: Server Error - An unexpected error occurred
                      while processing the request

                      - **501**: Generation Failed - Content generation task
                      failed

                      - **505**: Feature Disabled - The requested feature is
                      currently disabled
                  msg:
                    type: string
                    description: Response message, error description when failed
                  data:
                    type: object
                    properties: {}
                    x-apidog-orders: []
                    x-apidog-ignore-properties: []
                x-apidog-orders:
                  - code
                  - msg
                  - data
                required:
                  - code
                  - msg
                  - data
                x-apidog-ignore-properties: []
              example:
                code: 500
                msg: >-
                  Server Error - An unexpected error occurred while processing
                  the request
                data: null
          headers: {}
          x-apidog-name: 'Error '
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
      x-apidog-folder: docs/en/Market/Suno API/Music Generation
      x-apidog-status: released
      x-run-in-apidog: https://app.apidog.com/web/project/1184766/apis/api-28506289-run
components:
  schemas: {}
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
