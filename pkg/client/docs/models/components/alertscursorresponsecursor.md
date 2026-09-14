# AlertsCursorResponseCursor

Paginated cursor wrapping the list of alerts


## Fields

| Field                                                  | Type                                                   | Required                                               | Description                                            |
| ------------------------------------------------------ | ------------------------------------------------------ | ------------------------------------------------------ | ------------------------------------------------------ |
| `PageSize`                                             | `int64`                                                | :heavy_check_mark:                                     | N/A                                                    |
| `HasMore`                                              | `bool`                                                 | :heavy_check_mark:                                     | N/A                                                    |
| `Previous`                                             | `*string`                                              | :heavy_minus_sign:                                     | N/A                                                    |
| `Next`                                                 | `*string`                                              | :heavy_minus_sign:                                     | N/A                                                    |
| `Data`                                                 | [][components.Alert](../../models/components/alert.md) | :heavy_check_mark:                                     | N/A                                                    |