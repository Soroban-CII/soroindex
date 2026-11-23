// Package client provides a typed Go client for the sep47idx /v1 HTTP API.
//
// Example usage:
//
//	client := client.NewClient("http://localhost:8080")
//
//	// List all contracts declaring SEP-41.
//	page, err := client.Contracts(ctx, client.ContractsFilter{
//	    Implements: []int{41},
//	    Tier:       "declared",
//	})
//	for _, c := range page.Contracts {
//	    fmt.Println(c.ID, c.Kind)
//	}
//
//	// Iterate through all pages automatically.
//	it := client.ContractsIterator(ctx, client.ContractsFilter{
//	    Tier: "inferred",
//	})
//	for it.Next() {
//	    for _, c := range it.Contracts() {
//	        fmt.Println(c.ID, c.WasmHash)
//	    }
//	}
//	if err := it.Err(); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Get a single contract detail.
//	detail, err := client.GetContract(ctx, "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAK")
//	...
//
//	// Stream contract upgrades via SSE.
//	err = client.UpgradesStream(ctx, 0, func(change client.VersionChange) error {
//	    fmt.Printf("Contract %s upgraded at ledger %d\n", change.ContractID, change.Ledger)
//	    return nil
//	})
package client
