// A .sql file imported as a string (the asset/source rule in next.config.ts).
declare module "*.sql" {
  const sql: string
  export default sql
}

// webpack's require.context, used to list tools/lettering-duckdb/queries.
declare namespace NodeJS {
  interface Require {
    context(
      directory: string,
      recursive: boolean,
      pattern: RegExp
    ): { keys(): string[]; (id: string): string }
  }
}
