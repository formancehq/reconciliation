# Ack

Record of who acknowledged an alert and when


## Fields

| Field                                                 | Type                                                  | Required                                              | Description                                           | Example                                               |
| ----------------------------------------------------- | ----------------------------------------------------- | ----------------------------------------------------- | ----------------------------------------------------- | ----------------------------------------------------- |
| `By`                                                  | `string`                                              | :heavy_check_mark:                                    | Who acknowledged the alert                            | ops@buildr.com                                        |
| `At`                                                  | [time.Time](https://pkg.go.dev/time#Time)             | :heavy_check_mark:                                    | When the alert was acknowledged                       |                                                       |
| `Note`                                                | `*string`                                             | :heavy_minus_sign:                                    | Free-text note left by whoever acknowledged the alert |                                                       |