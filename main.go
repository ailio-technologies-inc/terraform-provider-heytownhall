// Terraform provider for Hey Town Hall Data.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/provider"
)

//go:generate go tool tfplugindocs generate --provider-name heytownhall --rendered-provider-name "Hey Town Hall Data"

// version is set by the release build (goreleaser).
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/ailio-technologies-inc/heytownhall",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
