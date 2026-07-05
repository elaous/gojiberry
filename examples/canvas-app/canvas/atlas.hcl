env "local" {
  src = "file://schema.pg.hcl"
  dev = "docker://postgres/16/dev?search_path=public"

  migration {
    dir    = "file://migrations"
    format = golang-migrate
  }
}
