data "heytownhall_coverage" "bc" {
  country = "CA"
  region  = "BC"
}

output "bc_jurisdictions" {
  value = [for j in data.heytownhall_coverage.bc.jurisdictions : j.name]
}
