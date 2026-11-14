# The current month.
data "heytownhall_usage" "now" {}

# A past month.
data "heytownhall_usage" "october" {
  month = "2026-10"
}

output "bill_to_date_dollars" {
  value = data.heytownhall_usage.now.total_cents / 100
}
