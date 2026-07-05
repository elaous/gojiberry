table "documents" {
  schema = schema.public
  column "id" {
    null = false
    type = uuid
  }
  column "content" {
    null    = false
    type    = text
    default = ""
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  column "updated_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
}

schema "public" {
  comment = "standard public schema"
}
