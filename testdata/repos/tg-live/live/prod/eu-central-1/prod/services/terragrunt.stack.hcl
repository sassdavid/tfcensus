# The units come from the local catalog next to this repo, so stack generate needs no network.
unit "api" {
  source = "../../../../../../tg-catalog/units/api"
  path   = "api"

  values = {
    name = "api"
  }
}

unit "worker" {
  source = "../../../../../../tg-catalog/units/worker"
  path   = "worker"

  values = {
    name = "worker"
  }
}
