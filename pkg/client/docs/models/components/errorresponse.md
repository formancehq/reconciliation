# ErrorResponse

Error response


## Fields

| Field                                                     | Type                                                      | Required                                                  | Description                                               | Example                                                   |
| --------------------------------------------------------- | --------------------------------------------------------- | --------------------------------------------------------- | --------------------------------------------------------- | --------------------------------------------------------- |
| `ErrorCode`                                               | `string`                                                  | :heavy_check_mark:                                        | Machine-readable error code identifying the failure       | VALIDATION                                                |
| `ErrorMessage`                                            | `string`                                                  | :heavy_check_mark:                                        | Human-readable description of the error                   |                                                           |
| `Details`                                                 | `*string`                                                 | :heavy_minus_sign:                                        | Optional link carrying additional context about the error |                                                           |