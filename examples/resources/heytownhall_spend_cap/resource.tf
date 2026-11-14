# Cap live spending at 500 US dollars a month. Leave `cents` out for no cap.
resource "heytownhall_spend_cap" "monthly" {
  cents = 50000
}
