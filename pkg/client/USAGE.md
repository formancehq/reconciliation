<!-- Start SDK Example Usage [usage] -->
```go
package main

import (
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
	ctx := context.Background()

	s := client.New(
		"https://api.example.com",
		client.WithSecurity("<YOUR_API_KEY_HERE>"),
	)

	res, err := s.Reconciliation.V1.GetServerInfo(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if res.ServerInfo != nil {
		// handle response
	}
}

```
<!-- End SDK Example Usage [usage] -->