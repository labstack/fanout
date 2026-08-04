env "local" {
  src = "file://schema.sql"
  url = "sqlite://../../data/control/fanout.sqlite"
  dev = "sqlite://dev?mode=memory"

  migration {
    dir = "file://migrations"
  }

  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
