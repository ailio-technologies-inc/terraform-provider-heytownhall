terraform {
  required_providers {
    heytownhall = {
      source  = "ailio-technologies-inc/heytownhall"
      version = "~> 0.1"
    }
  }
}

# The token comes from the HEYTOWNHALL_MANAGEMENT_TOKEN environment variable.
# Create one in the dashboard (an organisation owner can). It starts with htd_mgmt_.
provider "heytownhall" {}

# Or pass it from a sensitive variable.
variable "heytownhall_token" {
  type      = string
  sensitive = true
}

provider "heytownhall" {
  alias = "explicit"
  token = var.heytownhall_token
}
