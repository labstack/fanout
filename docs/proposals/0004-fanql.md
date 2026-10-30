# Full UI without an LLM key

Status: Accepted (section 0, FanQL, implemented in #410; sections 1 to 5 pending)
Date: 2026-10-29

## Intent

Fanout must be fully usable when no AI provider is configured (`FANOUT_AI_PROVIDER` empty). Today creating a dashboard, adding a panel, exploring data, searching logs, finding traces and managing alerts all go through chat or MCP. A self-hosted user without a key, or a hosted fanout.run customer who has not entered one, gets read-only dashboards plus SQL edits on existing panels. Chat stays the fastest path when a key exists; it must not be the only path.

## Goals

- Every capability chat offers for dashboards, queries, logs, traces and alerts has a direct UI path that works without AI.
- One query model: the existing structured panel query (`from`, `where`, `measures`, `by`, `bucket`, `histogram`, `sort`, `limit`), viewed and edited as FanQL text (section 0) or in a visual builder, plus the existing read-only DuckDB SQL gate for anything the structured query cannot express.
- One renderer: every result renders through the existing `PanelCard` and panel engine. No second chart, table, log or trace path.
- AI affordances (Ask, Explain, Fix, the chat rail) are hidden when chat is disabled, so nothing is a dead button.

## Non-goals

- A new query engine. Queries run as SQL or the existing structured query; the text syntax and the builder are two views of the structured query.
- A new execution engine or a weaker SQL gate. The backend changes are the ones section 0 lists: measure expressions (`count_if`, arithmetic) with budgets in `internal/panel`, the extraction of `QueryShape` from panel validation, the shared severity table, and read-only catalog accessors. Every change keeps the existing SQL safety gate.
- Replacing chat or MCP flows. They keep working unchanged when a key exists.

## Anti-goals

- A parallel renderer or a second query path for the new pages.
- Weakening the SQL gate (one read-only `SELECT`, approved relations, function blocklist, `$__window`) to make the editor easier.
- UI that shows AI controls which fail when no key is configured.

## What exists (verified in the current tree)

- Routes: chat, dashboards list and detail, settings (`ui/host/src/routes/`).
- Dashboards: view, layout, duplicate and remove panels, and "Edit SQL" with a run preview (`ui/host/src/dashboards/panel-sql-editor.tsx`, `grid.tsx`). "Create dashboard" opens chat with `createDashboardPrompt` (`ui/host/src/shell.tsx:54`).
- APIs: `POST /api/panels/query` (runs a dashboard spec, used by the SQL editor preview with a one-panel spec), `GET /api/telemetry/schema`, `POST /api/panels/exemplars`, `POST /api/panels/variables/resolve`, `GET /api/traces/:id`, dashboards CRUD with typed edit operations (`add_panel`, `update_panel`, `remove_panel`, `move_panel` in `internal/dashboard/edit.go`), alerting rules CRUD and alert history (`/api/alerting/rules`, `/api/alerting/alerts`, `/api/alerting/summary`).
- Panel vizzes already include `logs`, `log_patterns`, `traces`, `service_map`, `table`, `timeseries` and others, with drill drawers for logs and traces.
- Alert rules use a CEL `expression` with service, namespace, `for_seconds`, cooldown, repeat interval and webhook fields (`internal/alert/types.go`).

## Design

### 0. FanQL, the text query syntax (owner-approved 2026-10-29; preview)

FanQL is the one-line text form of the structured panel query. Text parses into the same `panel.Query` that panels store and execute, and every valid query formats back to text; panels keep storing the query and the text is derived. One hand-written lexer and recursive-descent parser in Go (`internal/fanql`, with Pratt parsing for the boolean and arithmetic layers) serves the API, MCP and the UI; the UI calls an endpoint and ships no second parser. The syntax is preview: it may change before GA.

**Two contracts.** The *interactive language* is the text, bounded for editing (0.13). The *exact interchange form* is the structured `Query` itself: every envelope may carry it, and clients that cannot parse text use it. `Format` is total over valid queries and never truncates; text beyond the interactive bounds is returned complete and marked `editable: false` (0.11).

Everything below is normative. "Problem" means a diagnostic that blocks execution; "notice" means one that never does (0.12).

#### 0.1 Shape and entry points

```
source [where] filters | measures by fields every bucket sort key limit n
```

Explore takes a whole query. The Logs and Traces search bars take `filters` only; the page supplies the source and the viz, and a `where` typed there is the problem `the page supplies where`. The viz is a picker constrained by the query (0.12): an unaggregated span query is valid as `traces`; making it a timeseries by appending `| count() every auto` is a visible edit to the text, never an implicit transformation.

#### 0.2 Grammar

Terminals quoted; `[ ]` optional; `{ }` repetition; keywords are case-insensitive and recognised only in the positions 0.3 states. The lexer has two contexts, **filter** and **measure** (0.5, 0.10).

```
query       = source , [ "where" ] , [ filters ] , [ "|" , stage ] , { "|" , trailer } , [ sort ] , [ limit ] ;
source      = "spans" | "logs" | "metrics" ;                  (* first token only *)
trailer     = sort | limit ;                                 (* "| sort x", "| limit n" accepted; canonical drops the pipe *)

filters     = or_chain | conj ;                              (* an or chain is the whole list, or it is grouped *)
conj        = term , { [ "and" ] , term } ;
or_chain    = term , "or" , term , { "or" , term } ;
term        = ( "-" | "not" ) , term | primary ;             (* nested; depth bound *)
primary     = "(" , filters , ")"                            (* one entry; same grouping rule inside *)
            | raw
            | field , ":" , "*"                              (* exists *)
            | field , ":" , value                            (* equals, alias-mapped *)
            | field , ( "=" | "!=" | "<>" | ">" | ">=" | "<" | "<=" ) , value
            | field , ":" , ( ">" | ">=" | "<" | "<=" ) , value   (* accepted; canonical k>v *)
            | field , ( "~" | "!~" ) , value                 (* contains, case-insensitive *)
            | field , [ "not" ] , "in" , list
            | field , [ "not" ] , "between" , value , "and" , value
            | field , "is" , [ "not" ] , "null"
            | field , [ "not" ] , ( "like" | "ilike" ) , string
            | predicate_call
            | free_text ;
predicate_call = fname , "(" , [ carg , { "," , carg } ] , ")" , [ ( "=" | "!=" | "<>" | ">" | ">=" | "<" | "<=" ) , value ] ;
carg        = field | string | number | predicate_call ;
list        = "(" , value , { "," , value } , ")" | variable ;
free_text   = word | string ;
field       = bracket | "resource" , "." , ident , { "." , ident }
            | "attributes" , "." , ident , { "." , ident }
            | ident , "." , ident , { "." , ident }          (* attributes['a.b.c'] *)
            | ident ;                                        (* a column, or attributes['ident'] *)
bracket     = ( "attributes" | "resource" ) , "[" , string , "]" ;
value       = string | duration | number | boolean | variable | word ;
variable    = "$" , ident ;

stage       = measures , [ "by" , field , { "," , field } ] , [ "every" , bucket ]
            | "histogram" , "(" , field , ")" , [ "by" , field ] , [ "every" , bucket ] ;
measures    = measure , { "," , measure } ;
measure     = mexpr , [ "as" , alias ] ;                     (* alias required unless mexpr is one call;
                                                                at least one mcall per measure *)
mexpr       = mterm , { ( "+" | "-" ) , mterm } ;
mterm       = mfactor , { ( "*" | "/" ) , mfactor } ;
mfactor     = [ "-" ] , ( mcall | number | "(" , mexpr , ")" ) ;   (* unary minus *)
mcall       = mname , "(" , [ marg , { "," , marg } ] , ")"  (* mname in the measure registry; count(*) accepted *)
            | "count_if" , "(" , filters , ")" ;             (* filter context inside; one entry *)
marg        = field | number | "*" ;
bucket      = "auto" | "10s" | "30s" | "1m" | "5m" | "10m" | "15m" | "30m" | "1h" | "3h" | "6h" | "12h" | "1d" ;

sort        = "sort" , ( ( "+" | "-" ) , sort_key | sort_key , [ "asc" | "desc" ] ) ;
sort_key    = ident ;                                        (* measure alias; traces: duration, errors, +start; logs: time, +time; log_patterns: count *)
limit       = "limit" , integer ;
```

Lexical grammar, filter context:

```
ident     = ( letter | "_" ) , { letter | digit | "_" } ;
alias     = lower , { lower | digit | "_" } ;                (* 1..40; aliasPattern in measure.go *)
word      = wordchar , { wordchar } ;
wordchar  = any rune except whitespace and  | ( ) , : = ! < > ~ " ' ` [ ] ;
            "-" and "+" are wordchars except as the first rune of a token;
            "*" is a wordchar only as the whole token (k:*);
            "$" only as the first rune of a variable; "#" "{" "}" "@" are not wordchars ;
string    = '"' , { char - '"' | '""' } , '"' | "'" , { char - "'" | "''" } , "'" ;
raw       = "`" , { char - "`" | "``" } , "`" ;
number    = digit , { digit } , [ "." , { digit } ] , [ ( "e" | "E" ) , [ "+" | "-" ] , digit , { digit } ] ;
duration  = number , ( "us" | "ms" | "s" | "m" | "h" ) ;
boolean   = "true" | "false" ;
integer   = digit , { digit } ;
```

Lexical grammar, measure context (after `|` until `by`, `every`, `sort`, `limit`, a second `|` or the end; `count_if(` re-enters filter context until its matching `)`, parentheses inside strings and raw entries not counting):

```
ident     = as above ;
path      = ident , "." , ident , { "." , ident } ;            (* no whitespace; one Path token; an attribute path *)
number    = as above ;                                        (* a token starting with a digit is a number: 1.5, 1e3 *)
string    = as above ;  bracket keys as in filter context ;
punct     = "(" | ")" | "," | "[" | "]" | "+" | "-" | "*" | "/" ;
```

Tokens: `Word`, `Ident`, `Path` (measure context only), `String`, `Raw`, `BracketKey` (a `String` immediately preceded by `attributes[` or `resource[`), `Number`, `Punct`, `Bad`, `EOF`; every token has a code-point span. The lexer never aborts; delimiters are preserved in the stream and matching is the parser's job. In measure context `+ - * /` are always operators regardless of spacing, `*` is legal only as the sole argument of `count(`, `-` at the start of an `mfactor` is unary minus, a `.` not inside a number or between two identifiers is `Bad`, and a trailing `.` (`avg(user.)`) is `unexpected . after user; write the full key, e.g. user.id`.

#### 0.3 Keywords and contextual rules

| Group | Words | Recognised |
|---|---|---|
| Sources | `spans logs metrics` | first token only |
| Clause words | `where by every sort limit` and `\|` | depth 0, outside quotes and brackets, at a term boundary |
| Expression words | `and or not in between is null like ilike` | their grammatical positions only |
| Modifiers | `as asc desc` | after a measure; after a sort key |
| Buckets | `auto` and the twelve buckets | after `every` |
| Stage names | `histogram`, `count_if`, the measure functions | after `\|` and `,` |
| Reserved | `let join correlate compare since until top eval extend having fields rename over window` | clause position only; `"join" is reserved for a future FanQL feature`, hint `quote it to search for the word` |
| Foreign | see below | unmistakable syntax only |

Rules, each with fixtures in the corpus:

1. **Source.** `spans`, `logs`, `metrics` are recognised only as the first token; elsewhere they are words. Missing or unknown: `unknown source "X"`, hint `use spans, logs or metrics`; `traces` gets `use spans; traces is a viz of spans`; `from spans` gets `"from" is not FanQL; start with the source: spans where ...`. A bare source (`logs`) is valid: all rows under the viz's defaults.
2. **Clause words** are clause words only at depth 0, outside quotes and brackets, at a term boundary. After `:`, a comparison, `in (`, or inside a list they are values (`service:where`, `service in (sort, limit)`). As free text they must be quoted (`logs where "limit"`); canonical quotes them. `logs where limit 10` is `where needs a filter`, hint `quote "limit" to search for the word, or drop where`. `where` twice is `where appears twice; combine the filters into one where`.
3. **Expression words.** `not` at term start; `and`/`or` between terms (0.4); `in between is like ilike` after a field; `null` after `is [not]`; `and` inside `between`, which consumes its own `and` before conjunction parsing. Elsewhere they are ordinary words (`logs where in` searches "in"). A missing upper bound is `between needs two values`.
4. **Field vs free text.** A word is a field only when the next token is `:`, a comparison operator, `~`/`!~`, or one of `in`, `not in`, `between`, `not between`, `is`, `like`, `ilike`, `not like`, `not ilike`. Otherwise it is free text, whether or not it names a column (`logs where service` searches `body` for "service", with the notice `"service" is a field; did you mean service:<value>?`). Meaning never depends on schema discovery.
5. **`:` ends a word.** `http.target:/a:b` tokenises as `http.target` `:` `/a` `:` `b`; the parser reports `unexpected : after /a`, hint `quote the value: http.target:"/a:b"`, over the whole whitespace-delimited run. The same for `http://x:8080/y` typed as free text.
6. **Comparison punctuation** `: = != <> > >= < <= ~ !~` are their own tokens, longest match first; whitespace around them is irrelevant. A bare `!` is `! negation is not FanQL`; `~~` is `~~ is SQL LIKE; use like, ilike or ~`.
7. **Signs.** After a value-expecting operator (`:`, a comparison, `in (`, `,` in a list, `between` bounds) a `-` followed by a digit or `.digit` is a numeric sign (`value>-3`; `duration>-1ms` is then `durations are not negative`). Anywhere else a leading `-` is negation (`-duration>1s` negates the term). `+` at token start is legal only after `sort` and in exponents; elsewhere `unexpected +`. Inside a word both are ordinary (`us-east-1`, `web+api`). Repeated negation parses (`--a`, `not not a`, `-not a`, depth-bounded); canonical prints one `-` for odd, none for even.
8. **Value classification.** In value position the classifier tries duration, then number, then boolean, then word. In field position a number-shaped token is `a filter needs a field before >`.
9. **Calls.** An identifier immediately followed by `(` is a call. In `where` the name must be a filter function (`lower upper starts_with contains regexp_matches length`); `spans where P95(Duration)` is `function p95 is not allowed in a filter`. Arguments are typed by signature: `starts_with(field, string)`, `contains(field, string)`, `regexp_matches(field, string)`, `lower(field)`, `upper(field)`, `length(field)`. A bare word in a `string` slot is `starts_with needs a quoted string`. `length`, `lower`, `upper` must be compared (`length(...) needs a comparison`); `starts_with`, `contains`, `regexp_matches` are predicates alone. A space before `(` is `write starts_with(...) without a space`.
10. **Optional `where`.** `where` may be omitted after the source; canonical prints it whenever a filter is present. Search bars never contain it and reject it.
11. **Foreign syntax** gets a hint, never an alias, and only on unmistakable syntax: a known foreign stage command directly after `|` (`stats summarize select project extend eval fields rename take head top order sort by`), `order by`/`group by`/`facet` at a clause position, a foreign call shape at stage position (`bin( span( percentile( perc95( pct( count_over_time( quantile_over_time( avg( mean( median( dc( distinct_count( cardinality(`), `from`, `index=`, `source=`, `search` as the first token, `ago(`, `now(`, `earliest=`, `_time:`, `since`/`until` at clause position, the operators `== =~ |= |~ ::`, `{` at term start, `@word:`, `!word:`. A bare foreign word in filter position is free text. Messages name the FanQL form: `"summarize" is not FanQL; write the measures after |, e.g. | count() by service`; `== is not FanQL; use key:value or =`; `=~ is not FanQL; ~ is contains; use regexp_matches(field, 're') for a regular expression`; `{ selectors are not FanQL; write labels as key:value, e.g. service:api`; `"@http.target" is not a field name; write the attribute without @: http.target`; `! negation is not FanQL; write -service:api`; `the time range is not part of FanQL; use the time picker (since is reserved)`.
12. **Quotes, backticks, brackets.** `"` or `'` with doubling as the only escape (no backslash escapes). Unclosed quote: `unclosed quote; close the value with "`, running to the end of the text. Backticks open a raw entry (0.8); `unclosed raw filter; close it with `` ` ``. Parentheses are preserved in the token stream: `unmatched ); remove it or add the opening (` and `unclosed (; add ) before |, sort, limit or the end` with the opening span. Recovery after a bad term consumes to the next whitespace at the term's depth.
13. **`*` and `$`.** `*` is legal as `k:*`, `-k:*`, `count(*)`; elsewhere in an unquoted value it is `* wildcards are not in FanQL`, hint `use ~ for contains, or quote "api*"`. `$ident` is a variable; `$` followed by anything else is `unexpected $`; `$__` names are rejected.
14. **Second `|`.** `| sort x` and `| limit n` after the stage are accepted and canonicalised without the pipe. Any other second stage: `FanQL runs one aggregation stage`, hint `remove the second |; more stages come in a later version`.
15. **Sort.** `sort key`, `sort +key`, `sort -key`, `sort key asc`, `sort key desc`. Two directions is `sort takes one direction`. Canonical: `sort key` (descending) or `sort +key`. `limit` before `sort` is `limit comes before sort here; write ... sort duration limit 10`. The key is checked against the stage's aliases (`sort "p95" must name a measure alias; aliases here: count`) or the viz's row keys (0.12).
16. **Case.** Keywords, function names, column names, units and vocabulary aliases fold case. Values, free text and attribute/resource keys keep case (`HTTP.Target` is the key `HTTP.Target`).
17. **Limits** (0.13) are each reported at a span; `limit 0` is `limit needs a whole number from 1 to 1000; e.g. limit 50`; `every 2m` is `unknown bucket "2m"; use auto or one of 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h, 1d`; `| every 1m` alone is `every needs measures first; write | count() every 1m`; a fourth `by` field is `at most 3 grouping fields`.

#### 0.4 Mandatory grouping for `and` and `or`

Highest first: `( )`; `-`/`not`; comparison operators; then one level holding both `or` and `and`/juxtaposition, which **do not mix**. A filter list, the inside of a group, and the inside of `count_if` is either a conjunction or an `or` chain, never both. There is no precedence between `or` and `and`.

- `a or b` is one entry `(a OR b)`; `a (b or c)` is entries `a`, `(b OR c)`; `(a b) or c` is one entry `((a AND b) OR c)`; `a or (b c)` is one entry; `not a b` is `(NOT a)`, `b`; `-(a or b)` is one entry.
- `a b or c`, `a and b or c`, `a or b c`, `(a b or c)`, `count_if(a b or c)` are the problem `mixed and/or needs parentheses` at the span from the first term to the last, hint `write a (b or c) or (a b) or c`, with a fix-it carrying both texts. Nothing is executed.

The Pratt loop records the first binary operator kind seen at a level and reports the problem on seeing the other kind. The printer, completion and the fixtures follow this one grammar. Readers arrive with both habits (Splunk search evaluates `OR` before `AND`; Lucene and Kibana KQL the reverse), and the entry boundary that All depends on (0.6) makes a silent choice costly.

#### 0.5 Fields

Field position means a `where` term read as a field (0.3 rule 4), a measure argument, `histogram(…)` and the fields after `by`; one resolution rule serves all four:

- A word of identifier segments is an attribute path: `http.target` is `attributes['http.target']`; `resource.<path>` is `resource['<path>']`; `attributes.<path>` is `attributes['<path>']` (canonical drops the prefix). A bracket key (`attributes['k']`, `resource["k"]`) is literal; the printer never canonicalises a bracket key into a prefix form when the prefix would change the map, so an attribute whose key begins `resource.` is reachable only as `attributes['resource.k']`.
- A **single-segment** identifier is a column when the source has one, else `attributes['ident']`, **never an error**: `spans where value>-3` is `attributes['value'] > -3` and `metrics where rate:x` is `attributes['rate'] = 'x'`. When no column matches, a notice names the reading (`"value" is not a spans column; reading attributes['value']`; for a measure name, `"rate" is a measure, not a metrics column; reading attributes['rate']`) and adds `did you mean service?` when a column is one edit away. Notices never block, so meaning never depends on discovery.
- Canonical prints the **bracket form** for single-segment attribute keys (`attributes['flag']:true`, `count_distinct(attributes['flag'])`) and the bare form for dotted keys (`http.target:/h`, `count_distinct(user.id)`). Adding a column therefore never changes the meaning of canonical or stored text; it changes hand-typed bare text and must be reviewed against the corpus (0.13).
- Registry invariant, tested: column names contain no `.`, so a dotted key is never a column. A segment that is not an identifier (`x-y.z`) is `"x-y.z" needs brackets`; in measure context, where `-` is an operator, `count_distinct(x-y)` is `unexpected - in count_distinct; write attributes['x-y']`.
- `duration` is `duration_ms` in filters, measures and `sort`; `time`, `start_time`, `end_time` are time columns (`"time" is a time column; compare it with > or <`).
- In measure context a dotted word is one `Path` token (`count_distinct(user.id)`, `avg(attributes.http.response_size)`, `p95(resource.k)`); a token starting with a digit is a number (`count() * 1.5`). After `by` the lexer is back in filter context, so `by resource.k8s.pod.name` is a word read as a field.
- In value position a dotted word is a value (`name:http.server.duration`).

#### 0.6 Entries and variables

The structured query stores `Where []string`; the compiler ANDs the entries and treats each entry as a unit: an entry referencing a variable set to All is removed whole; an entry whose variable has zero values compiles to `FALSE`; metric aggregates need `name = literal` as a standalone entry; deploy split, rollup scope, annotation scope, empty-panel diagnosis and `inferBetter` all read standalone equalities. FanQL therefore makes entry boundaries part of the meaning:

- The tree's `Where` is an ordered list of entries. At the top level of `filters`, each whitespace- or `and`-separated term is one entry; a parenthesised group is one entry; a raw entry is one entry; an `or` chain is one entry and must be the entire filter list (otherwise it is the grouping problem of 0.4).
- `where a b (c or d) (e f)` lowers to four entries: `L(a)`, `L(b)`, `(L(c) OR L(d))`, `(L(e) AND L(f))`. `where a or b` is one entry `(L(a) OR L(b))`.
- The printer prints entries in stored order separated by single spaces, parenthesising an entry whose top node is `and` or `or` when there is more than one entry, and never merging or splitting entries. A lifted stored entry `service = $service AND status = 'STATUS_CODE_ERROR'` prints as `(service:$service status:error)`.
- Canonical text echoed after a run shows exactly the boundaries that will execute.

Variables set to All, per construct (no backend change):

| FanQL | Entries | Under `$service` = All |
|---|---|---|
| `service:$service status:error` | 2 | status filter kept |
| `(service:$service status:error)` | 1 | both dropped |
| `service:$service or service:worker` | 1 | whole entry dropped |
| `service:$service (status:error or status:unset)` | 2 | status group kept |
| `-service:$service` | 1 | dropped (nothing excluded) |
| `` `service = $service AND x` `` raw | 1 | dropped |
| `count_if(service:$service)` | n/a | rejected: `variables are not allowed inside count_if` |

The reference states beside `$name`: "a filter that mentions a variable set to All is removed; keep fixed conditions in their own terms". `$service` lowers as `$service`; `k in $multi` lowers to `k IN $multi`; `service:$multi` for a multi variable is `$multi holds several values; use service in $multi`. With `variables` supplied, an undeclared `$x` is a problem with the declared list; without them every `$x` is the notice `declared variables are needed to check $x`, so the search bars can parse, and the run goes through `Check`.

#### 0.7 Null semantics and negation

`attributes['k']` is NULL when the key is absent; SQL `k != 'v'` on NULL is NULL and excludes the row. FanQL predicates are two-valued ("matched" or "not matched") and negation is negation of "matched". Negation is pushed to the leaves (negation normal form: `-(a b)` is `(-a) or (-b)`, `-(a or b)` is `(-a) (-b)`, `--a` is `a`); positive leaves are never wrapped, so the standalone-equality shape the panel depends on survives.

**Rule: prefix negation (`-`, `not`, and FanQL's own `!~`) includes rows missing the field; SQL-spelled infix negation (`!=`, `<>`, `not in`, `not like`, `not between`) keeps SQL three-valued semantics**, so stored SQL negatives lift exactly.

| Row has `k = v` | `k = w` | `k` missing | FanQL | Lowered |
|---|---|---|---|---|
| T | F | F | `k:v`, `k=v` | `k = 'v'` |
| F | T | **T** | `-k:v`, `not k:v` | `(k IS NULL OR k != 'v')` |
| F | T | F | `k!=v`, `k<>v` | `k != 'v'` (SQL) |
| T | F | **T** | `-k!=v`, `-k<>v` | `(k IS NULL OR k = 'v')` |
| T | F | F | `k ~ v` | `contains(lower(k), lower('v'))` |
| F | T | **T** | `-k ~ v`, `k !~ v` | `(k IS NULL OR NOT contains(lower(k), lower('v')))` |
| T | F | F | `k in (v, x)` | `k IN ('v', 'x')` |
| F | T | **T** | `-k in (v, x)` | `(k IS NULL OR k NOT IN ('v', 'x'))` |
| F | T | F | `k not in (v, x)` | `k NOT IN ('v', 'x')` (SQL) |
| T | F | **T** | `-k not in (v, x)` | `(k IS NULL OR k IN ('v', 'x'))` (a different filter from `-k in`) |
| F | F | T | `-k:*`, `k is null` | `k IS NULL` |
| T | T | F | `k:*`, `k is not null` | `k IS NOT NULL` |
| per bounds | | **T** | `-k between a and b` | `(k IS NULL OR k NOT BETWEEN a AND b)` |
| per bounds | | F | `k not between a and b` | `k NOT BETWEEN a AND b` (SQL) |
| per bounds | | **T** | `-k not between a and b` | `(k IS NULL OR k BETWEEN a AND b)` |
| per cast | | **T** | `-http_status_code between 400 and 599` | `NOT coalesce((TRY_CAST(http_status_code AS INTEGER) BETWEEN 400 AND 599), FALSE)` |
| per pattern | | **T** | `-k like "p"` | `(k IS NULL OR k !~~ 'p')` |
| per pattern | | F | `k not like "p"` | `k !~~ 'p'` (SQL) |
| per pattern | | **T** | `-k not like "p"` | `(k IS NULL OR k ~~ 'p')` |

**The complete leaf rule.** The authoritative lowering of a negated leaf is `NOT coalesce((p), FALSE)`, where `p` is the leaf's positive lowering including any cast or function it wraps around the field (its effective predicate). The shorter `(k IS NULL OR <NOT-form>)` spelling is a permitted rewrite of that form, used only when the effective operand is the stored field itself or a rename or cast that is NULL exactly when the field is NULL (`duration_ms` for `duration`; `CAST(attributes['k'] AS VARCHAR)` inside `contains`). The effective operand decides first; the simple spelling never overrides it.

| Leaf class | Negated lowering |
|---|---|
| Equality, membership, contains, between, like, comparison whose operand is the stored field | `(k IS NULL OR NOT-form)` as in the table |
| Prefix negation of an SQL-negative leaf on the stored field (`-k!=v`, `-k not in`, `-k not like`, `-k not between`) | `(k IS NULL OR k = 'v')`, `(k IS NULL OR k IN (…))`, `(k IS NULL OR k ~~ 'p')`, `(k IS NULL OR k BETWEEN a AND b)` |
| Existence | `-k:*` is `k IS NULL` |
| Any leaf whose positive lowering wraps the field in `TRY_CAST` or a function: `http_status_code` under `< <= > >=`, `between`, `not between`; attribute `k>5` through a cast; predicate calls and compared calls (`-starts_with(http_route, "/api")`, `-length(body) > 1000`) | `NOT coalesce((p), FALSE)`, the only form; `http_status_code IS NULL` alone would miss a present non-numeric value whose cast is NULL |
| Raw entry `` -`expr` `` | `NOT coalesce((expr), FALSE)` |
| Free text `-word` | `(body IS NULL OR NOT contains(lower(body), lower('word')))` |
| Variable comparison `-k:$v` | `(k IS NULL OR k != $v)`; under All the entry is dropped |

The reference says beside `!=`: "rows without `k` are excluded; `-k:v` includes them", and beside `not in`/`not like`/`not between`: "SQL spelling, SQL meaning. To exclude these values and include rows missing the field, put `-` before the positive predicate: `-k in (v)`. `-k not in (v)` is a different filter: it negates the SQL-negative predicate and matches rows where `k` is `v` or `k` is missing." Completion offers the `-k in` form after `-`. For columns that are never NULL the forms agree; the `IS NULL` branch is still emitted so the rule has no exceptions. `NOT`, `coalesce`, `IS NULL`, `IN`, `BETWEEN` are all accepted by the filter gate (`filterOperators` in `filter.go`).

#### 0.8 Literals, contains, raw entries

| Literal | Form | Typing rule |
|---|---|---|
| String | `"a b"`, `'a b'`, bare `api`, `/healthz`, `us-east-1` | Quote doubling; no backslash escapes. A quoted value is always a string and is never alias-mapped (`status:"STATUS_CODE_ERROR"`, `severity:"error"`). |
| Number | `500`, `1.5`, `2e3`, `-3` | On a number column: number. On a string column: quoted string (`http_status_code:500` is `'500'`). On an attribute: number as typed (`attributes['k'] = 500`); quote to force text. |
| Boolean | `true`, `false` (case-insensitive, bare) | On an attribute: boolean literal; on a string column: `'true'`; on a number column: `duration_ms needs a number`. |
| Duration | `500ms`, `1.5s`, `250us`, `2m`, `1h` | Only on `duration`; converted to milliseconds; no compound `1h30m`; negative rejected. `duration>500` is `duration needs a unit; e.g. duration>500ms` (`duration_ms>500` takes a bare number). On any other field: `units apply to duration only; write http_status_code>500`. |
| Timestamp | `"2026-10-29T10:00:00Z"`, `"2026-10-29 10:00:00"` (quoted; optional fraction and offset; no offset means UTC) | Only on time columns; lowered to `'…'::TIMESTAMPTZ_NS`. Elsewhere a string. |
| Variable | `$service` | 0.6 |
| Existence | `k:*` | `IS NOT NULL`; `-k:*` is `IS NULL`. The only accepted `*` in a value. |
| Free text | `timeout`, `"connection reset"` | `contains(lower(body), lower(text))` on logs, `operation` on spans; metrics: `free text needs a field on metrics; use name:http.server.duration`. |
| Raw | `` `expr` `` | below |

**`http_status_code`** is a text column. Comparisons `< <= > >=` and `between` on it lower through `TRY_CAST(http_status_code AS INTEGER)` so `>=500` is numeric; equality and `in` stay on the text column (`= '500'`) so the standalone-equality shape survives. One catalog flag (`compareAs: integer`) on one field.

**Contains.** One helper produces every substring test: `containsSQL(field, value) = contains(lower(<field>), lower(<value>))`, where `<field>` is the column or `CAST(attributes['k'] AS VARCHAR)` for attribute and resource lookups (emitted unconditionally), and `<value>` is a string literal with `'` doubled or `$name`. It serves `k ~ v`, `-k ~ v`, `k !~ v`, free text and `-word`; `%` and `_` in the value are ordinary characters. `like`/`ilike` are explicit SQL-pattern operators (`k like "GET %"` is `k ~~ 'GET %'`; `ilike` is `~~*`) so lifted agent filters print without raw; `ESCAPE` is not expressible, and a pattern needing a literal `%` uses `~`.

**Raw entries.** `` `expr` `` is one entry passed to `checkFilter` verbatim; inside, a doubled backtick is a literal backtick and a single backtick ends the entry, so any string is encodable (`` `coalesce(body, '``') = ''` ``). Raw entries print exactly as stored. The reference lists what the gate accepts: columns, `attributes['k']`/`resource['k']`, comparisons, boolean logic, `IN`, `LIKE`, `IS NULL`, `BETWEEN`, `coalesce`, casts to `VARCHAR BIGINT INTEGER DOUBLE BOOLEAN UBIGINT TIMESTAMPTZ_NS`, `lower upper starts_with contains regexp_matches length`, `$variables`. Not accepted, and so not expressible even raw: `CASE`, `DISTINCT`, `ORDER BY` and `FILTER` inside functions, qualified column names, other functions.

#### 0.9 Vocabularies and severity

Aliases apply only under `:`, `=`, `!=`, `<>`, `-`/`not`, `in`, `not in`. Under `~`, `like`, `ilike`, inside function calls, and whenever the value is quoted, the value is literal. The stored spelling is always accepted bare too (`status:STATUS_CODE_ERROR`); canonical prints the alias. Exception, by name: under `<`, `<=`, `>`, `>=` and `between`, a `severity` operand is a level name and is rank-mapped; every other field's range operand is literal.

| Field | Aliases (input, case-insensitive) | Stored |
|---|---|---|
| `status` (spans) | `error ok unset` | `STATUS_CODE_ERROR` `STATUS_CODE_OK` `STATUS_CODE_UNSET` |
| `kind` (spans) | `server client internal producer consumer unspecified` | `SPAN_KIND_*` |
| `severity` (logs) | canonical `trace debug info warn error fatal unspecified`; ingestion synonyms `warning` (warn), `critical crit emerg emergency alert panic` (fatal), `err severe exception` (error), `information informational notice` (info), `dbg fine` (debug), `verbose finest` (trace); trailing digits dropped (`INFO2`) | the canonical name, uppercase |
| `type` (metrics) | `gauge counter updown histogram summary` | same |
| `duration` | unit suffixes | `duration_ms` |

Any other word under `severity:` is uppercased, as ingestion does for unknown levels, with the notice `not a standard level; stored levels in this window: …` when schema values are available. `status:fail` is `unknown status "fail"; use status:error, status:ok or status:unset`; `kind:http` likewise.

The severity alias table is **the same table ingestion uses**: `normalizeSeverity`, `severityFromText` and the rank order move to `internal/telemetry/severity.go` and are imported by `ingest` and `fanql`; no behaviour change in ingestion.

**Bands and ranges.** Every stored log row's `severity` is `normalizeSeverity(text, number)`: a positive `severity_number` determines the band (≥21 FATAL, ≥17 ERROR, ≥13 WARN, ≥9 INFO, ≥5 DEBUG, ≥1 TRACE); only an absent or nonpositive number falls back to the text mapping, which yields a band, `UNSPECIFIED`, or the emitter's own uppercased word. The stored column is therefore the effective band, computed once with number authority, and ranges compare it:

| FanQL | Lowered |
|---|---|
| `severity>=warn` | `severity IN ('WARN', 'ERROR', 'FATAL')` |
| `severity>warn` | `severity IN ('ERROR', 'FATAL')` |
| `severity<error` | `severity IN ('TRACE', 'DEBUG', 'INFO', 'WARN')` |
| `severity<=error` | `severity IN ('TRACE', 'DEBUG', 'INFO', 'WARN', 'ERROR')` |
| `severity between info and error` (inclusive) | `severity IN ('INFO', 'WARN', 'ERROR')` |
| `severity>fatal`, `severity<trace` | `severity>fatal matches no level; use severity:fatal` |
| `severity between error and info` | `between needs the lower level first: severity between info and error` |
| `severity>=custom`, `severity>=unspecified` | `"custom" has no severity rank; use trace, debug, info, warn, error or fatal` |

Consequences: WARN2 (number 14) is stored `WARN` and satisfies `>=warn` and `<error`; number 9 with text `ERROR` is stored `INFO` and fails `>=warn`; number 0 with text `FATAL` is stored `FATAL` and fails `<error`; `UNSPECIFIED` and unknown words never match a range; exact sublevels are reachable through `severity_number>=14`. The lowering contains no `CASE` (which the filter gate rejects) and lifts back: a stored `severity IN (…)` whose list is exactly a contiguous run of bands in ascending rank order prints as the range form; any other list prints as `severity in (…)`. The rule relies on one invariant, stated in the reference and guarded by a test: no stored log row bypasses `normalizeSeverity` (this product carries no legacy reader). `error_rate()` on logs uses `upper(severity) IN ('ERROR', 'FATAL', 'CRITICAL') OR severity_number >= 17`, which on normalized rows is exactly `severity>=error`; the reference says so beside both.

#### 0.10 Measures, distributions, expressions

Measures, as the reference documents them:

| Measure | Spans and logs | Metrics |
|---|---|---|
| `rate()` | rows per second over the bucket or window | counter increase per second with coverage weighting; counters only |
| `avg(value)` | n/a | gauge/updown point average; histogram `sum/count`; counter: no value, with diagnosis |
| `error_rate()` | percent of rows matching the error definition above | not available |
| `share()` | percent of the scope's rows in this group (window aggregate over the whole scope) | same idea over series |
| `count_distinct(f)` | any field, including string attributes | same |
| `last(f)` | not available | kind-aware last value |
| numeric measures on attributes | through `TRY_CAST(… AS DOUBLE)`; non-numeric values become NULL | same |

**Distribution form.** `| histogram(duration)` on spans and `| histogram(value)` on metrics lower to `Measures: ["count()"]` and `Histogram: {Field: "duration_ms", Buckets: "log2"}` or `{Field: "value", Buckets: "explicit"}`, the only two shapes validation accepts. `by field` (one; histogram viz) and `every bucket` (heatmap) follow the usual rules; `histogram(…)` cannot be combined with other measures (`distribution counts require count(); histogram(...) stands alone`). Lifting: `Histogram != nil` prints `histogram(f)`; a stored query with `Histogram` and measures other than `["count()"]` fails validation and is not formatted.

**Conditional aggregates and arithmetic** are in v1 for spans and logs on aggregate vizzes (stat, gauge, timeseries, bar, table, scatter, state_timeline); not for metrics (`measure arithmetic is not available for metrics`), distributions or `log_patterns`. The structured query carries them, so this is a backend change in `internal/panel`:

- `count_if(<filters>)` takes one FanQL filter list (same grammar and grouping rule as `where`, one entry); variables inside are rejected in v1; raw entries are allowed. Structured form: the expression with the inner filter in SQL, e.g. `count_if(status = 'STATUS_CODE_ERROR') / count() * 100 as err_pct`; compiled as `count(*) FILTER (WHERE <checked filter>)::DOUBLE`.
- `+ - * /` between measure calls, numeric constants and parenthesised expressions; unary minus. `/` lowers to `a / nullif(b, 0)`; a zero denominator yields NULL.
- An expression that is not a single call needs `as alias` (`name this expression with as, e.g. count() / 2 as half`). A single call keeps the auto-alias rules.
- **Every output expression has at least one aggregate leaf** (a call or `count_if`); numeric constants are operands only. `1 as one`, `1 / 2 as half`, `-(5) as neg` are `a measure needs an aggregate; constants can only scale one, e.g. count() * 100`; `count() * 0 as zero` is valid. The backend enforces the rule on structured `Query.Measures` too.
- **`share()` is a standalone measure only.** Inside an expression it is `share() cannot be combined; it is already a percentage of the scope`, hint `use share() alone, or count_if(...) / count() * 100 for a custom ratio`. Reason: paths that select rows before aggregating (table trends, the metrics trend branch, totals) detect `share` by its top-level function name to restore the whole-scope denominator; a `Binary` root would evade that detection.
- Units: a ratio of two count-family measures is `ratio`; `* 100` on a ratio is `percent`; `+`/`-` of equal units keep the unit; `*`/`/` with a constant keeps the unit; anything else is `none`. Expressions are not additive (no stacking).
- `inferBetter` extends to expressions recursively: a single call is today's rule; an affine `a·x + b` takes `x`'s inference, flipped when `a < 0`; a `ratio`/`percent` whose numerator is a `count_if` over an error filter is `lower`; a `count_if(error filter)` alone is `lower`; otherwise neutral.
- Apdex is written with disjoint predicates: `(count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex`; a span at exactly 500 ms counts once, as satisfied; the population is the rows surviving `where`; a NULL duration counts in `count()` and in neither numerator.

**Budgets** are enforced in `internal/panel` on every `Query.Measures[i]` string at parse time, whether it arrived from the builder, MCP, a stored dashboard or FanQL. They count tokens and nodes, never raw bytes, so every string accepted today (including whitespace-padded single calls) parses unchanged:

| Budget | Per measure | Per query |
|---|---|---|
| Tokens (whitespace is not a token) | 256 | — |
| Nodes (`Call`, `CountIf`, `Binary`, `Neg`, `Const`) | 64 | 256 |
| Nesting depth (`Binary`, `Neg`, parentheses together) | 8 (fails at 9 before recursing) | — |
| Aggregate leaves (`Call`, `CountIf`) | 8, and at least 1 | 24 |
| `count_if` filter text | 500 bytes (a `where` entry) | — |
| Measures | — | 6 |

Problems are reported at `panels[i].query.measures[j]` with the figure and the limit (`measures[2] has 70 nodes; a measure is at most 64`); FanQL maps them to the measure's text span. Raising a budget is additive; tightening one can reject previously accepted text and must be reviewed against the corpus.

**Ranking of grouped series.** Grouped timeseries and two-dimension bars select `top` series by ranking the first measure over the whole window; today's rules (`error_rate` by Wilson lower bound behind a 20-sample floor regardless of `better`; numeric measures by value behind the same floor counting actual samples; count-family by value) stay, and every order ends with the compiler's deterministic tie-breakers `n DESC, d` (dimension string ascending). Expressions attach ranking metadata instead of dispatching on the root:

| Expression shape | Support `n` | Eligibility | Order among eligible | Rule |
|---|---|---|---|---|
| Single registered call | as today | as today | as today | `builtin` |
| Affine of one call `g = a·f + b` (`a*f + b`, `f/c`, `-f`, `b - f`; `a ≠ 0`) | `f`'s | `f`'s | `f`'s own ranking expression, **untransformed**, with `f`'s direction computed from `better_g` mapped through the sign of `a` (flipped when `a < 0`); the coefficients are never applied to the score | `inherit` |
| Recognised binomial ratio `r = count_if(P) / count()`, optionally affine-scaled, same population | `count(*)` | `n >= 20` | by `better_r` on the unscaled proportion: `lower` → Wilson lower bound DESC; `higher` → Wilson upper bound ASC; neutral → Wilson lower bound DESC; `P` matching the error filter makes the default `lower` | `wilson` |
| Ratio with a `count_if` or `count(field)` denominator | the denominator's row count | `n >= 20` | `value <dir> NULLS LAST` | `value_floor` |
| Any other expression | `min` over aggregate-leaf supports (`count(field)` for numeric leaves, `count(*)` for count-family and `count_if`) | `n >= 20` | `value <dir> NULLS LAST` (`<dir>` is DESC unless `better` is `higher`) | `value_floor` |

Ineligible series (`n < 20`) follow eligible ones by `n DESC, d`; they are ranked last, not removed. Disclosure: `Result.Ranking = {rule, better: "lower" | "higher" | "neutral", floor: 20, support_column, below_floor: k, tie_breakers: ["n desc", "dimension asc"]}` and a frame note when `k > 0` (`k series had fewer than 20 samples and were ranked last`), shown where the UI shows "Other (N)". Consequences that are fixtures: `error_rate() + 0`, `error_rate() * 100`, `100 - error_rate()`, `-error_rate()` and `error_rate()` select identical series in identical order; `-p95(duration)` with `better: higher` selects what `p95(duration)` selects with `better: lower`; `100 - count_if(status:error) / count() * 100` with `better: higher` ranks the Wilson lower bound of the error proportion DESC.

#### 0.11 Round trip: format, lift, exactness, `editable`

`Format(q)` lifts a valid `panel.Query` to a FanQL tree and prints it. Definitions, comparing the re-lowered tree with `q` entry by entry using the internal lowering:

- **Byte-exact**: every `Where[i]` and `Measures[i]` equal as Go strings; `By`, `Bucket`, `Sort`, `Limit`, `Histogram`, `From` equal.
- **AST-exact (normalized)**: some `Where[i]` or `Measures[i]` strings differ while the engine's canonical rendering is equal: `RenderSQL(ParseSQL(lowered)) == RenderSQL(ParseSQL(stored))` for filters, and equality of the parsed measure (function, field, quantile, alias) for measures (so `p95( duration_ms )` formats as `p95(duration)`, normalized).
- **Raw**: a `Where[i]` whose tree contains a node FanQL has no construct for, or whose shorthand candidates all fail the exactness check, prints as a backtick entry; byte-exact by construction.
- **Equivalent** (same results on all data) is not a tier FanQL claims anywhere.

`Format` returns `exactness: "exact" | "normalized"` (raw entries are exact) and `notices` naming each normalized entry (`where[2] is shown as service:api; stored text is "service='api'"`). Saving from the editor writes the FanQL spelling; the UI shows the notice first. A query that fails shape validation under every viz is not formatted; `Format` returns its problems.

**Lifting through the engine's parse tree.** Stored strings are arbitrary author text and the engine's parse is the only authority on what they mean, so `Format` runs on the API side with an injected `panel.Parser`. Each `Where[i]` is parsed as `SELECT 1 FROM <from> WHERE (<where>)` (as `checkFilter` does) and the `where_clause` is mapped: `AND`/`OR` conjunctions to entries and groups; `NOT` to a candidate negation; comparisons with a field side and a constant or parameter side; `IN`/`NOT IN`; `BETWEEN`; `IS NULL`/`IS NOT NULL`; `~~ ~~* !~~ !~~*` to `like` forms; `contains(lower(F), lower(C))` to `~`; the engine's parse of `IN $p` to `in $p`; `attributes`/`resource` extracts and the `CAST … AS VARCHAR` inside `contains` to attribute fields; `TRY_CAST(http_status_code AS INTEGER)` under a comparison to `http_status_code`; timestamp casts to timestamps; `severity IN (contiguous bands)` to a range; `NOT coalesce((X), FALSE)` where `X` maps to a leaf to that leaf under prefix negation; the `(k IS NULL OR k = C)` family to `-k!=v`, `-k not in`, `-k not like`, `-k not between`. Anything else (`coalesce`, other casts, field-to-field, `lower(k) = 'v'`, `starts_with`, `regexp_matches`, `length`, `CASE`) is `Raw` for that entry. Measures lift through the backend expression parser; `duration_ms` prints as `duration`, attribute fields in their canonical spelling, `as alias` when stored; `Sort: "errors"` prints `sort errors`.

**Exactness gate and fallback.** The printer emits candidate text per entry and re-lowers it with the internal entry parser, whose interactive bounds are disabled (the bounded public `Parse` is never part of the gate). Shorthand candidates (`k:v`, `-k:v`, `k!=v`, `duration>500ms`, `k ~ v`, bare word, dotted key, vocabulary alias, severity range) are tried first and kept only if re-lowering is byte-exact or AST-exact. Whenever no candidate passes, **the whole original entry prints raw**, including recognised-but-incompatible trees: stored `NOT (attributes['k'] = 'v')` is raw (FanQL's only negation of `k:v` is null-inclusive); stored `NOT (k ~~ 'x%')` is raw while `k !~~ 'x%'` is `k not like "x%"`; stored `k NOT IN ('a', 'b')` is `k not in (a, b)`; stored `(attributes['k'] IS NULL OR attributes['k'] != 'v')` is `-k:v`; stored `NOT coalesce((attributes['k'] = 'v'), FALSE)` is raw (the permitted rewrite is canonical); stored `NOT coalesce((TRY_CAST(http_status_code AS INTEGER) BETWEEN 400 AND 599), FALSE)` is `-http_status_code between 400 and 599`; stored `(service IS NULL OR service IN ('a', 'b'))` is `-service not in (a, b)`. Format of a query FanQL itself produced never calls the engine: lowering is a pure function and the byte-exact check is a string comparison.

**`editable`.** `Format` carries `editable: true` when the text is within every interactive bound (0.13), else `editable: false` with `reason` (`formatted text is 15,466 bytes; the editor accepts 8,192`) and the complete text. The UI then shows the text read-only and keeps the builder on the original `Query`; Save writes that `Query` unchanged; the share action falls back to a saved view carrying `query` because a URL cannot carry the text. `Parse` enforces the interactive bounds before tokenising and reports the first one exceeded at a span. The **text-reparse property** `Lower(Parse(Format(q))) == q` under the reported tier is promised and tested only for `editable: true`; `editable: false` text is never submitted to `parse`, and its contract is preservation through the structured envelope (0.13). Oversized valid queries exist (sixteen 480-byte `body` literals; raw entries whose backticks double on escaping; measures padded with whitespace); nothing merges or drops entries to make them fit.

Guarantees, as tests: (1) every clean corpus text is a fixed point of `Format(Lower(Parse(t)))` with `exact` and `editable`; (2) every valid query in the default dashboards, the dashboard eval fixtures and the MCP tests formats with a tier and `editable`; when editable, `Lower(Parse(Format(q)))` equals `q` under the tier, `Check` results are identical and no entry count changes; when not, the text is complete, the envelope carries `q`, `q` survives JSON round trip, and the parser is never called; (3) property tests from FanQL's own lowering (always `exact`, `editable`, fixed point) and from the structured contract (tier in {exact, normalized, raw}, equal `Check` results and entry counts, `editable == false` exactly when a bound is exceeded); (4) a stored two-entry query and a stored one-entry `AND` query with the same predicates format to different texts and lower back to their own shapes; (5) each interactive bound is tested independently; (6) preservation for `editable: false` through the envelope, the builder and Save.

#### 0.12 Validation, problems and notices

**Shape checks.** `parse` and `complete` accept `viz` and `variables` (the dashboard's declarations), both optional; `format` accepts `variables` and an optional `panel`. The resolver lowers to `panel.Query` and calls a new exported `panel.QueryShape(q, viz, vars)`, extracted from the in-memory checks that depend only on the query, the viz and the declarations: from `validateQuery` (entry count and bytes, measure count and the grouped-one-measure rule, `by` bounds, bucket rules, histogram shape, limit, sort alias, unit families, reduce-one-measure), from `validateRows` (row-viz source, sort keys, `log_patterns` shape, no measures/by/bucket, no histogram), from `validateItems` (scatter two measures, `state_timeline` one measure and no sort), and variable references when declarations are supplied. `Requires` lists what full panel validity needs beyond the query: `min_max` for gauge, `thresholds` for `state_timeline`. Stacking additivity, highlight, option maxima and `Check`-only rules (filter binding, metric-name requirement, rollup representability, annotation projection) stay where they are; the FanQL handler maps `panels[0].query.where[i]` paths back to spans when `Check` runs on the transient panel.

The parse response reports shape problems with spans mapped from the lowered field back to the text (`measures[0]`, `where[2]`, `sort`, `bucket`, `by[1]`), and `viz.compatible: [{viz, requires: []}]` for every viz whose shape check passes **on the query as written**. `QueryShape` never normalizes; a timeseries without a bucket is reported as compatible-with-edit: `viz.edits: [{viz: "timeseries", text: "… every auto"}]`, applied only when the user accepts. Nothing returns a mutated query.

| Viz | Requires | Forbids |
|---|---|---|
| `logs`, `traces` | `from` logs / spans | measures, `by`, bucket, `histogram` |
| `log_patterns` | `from logs`, `by body_template`, `count()`; bucket auto or listed | a sort other than `count` |
| `timeseries` | 1-6 measures, bucket, `by` ≤ 1; one measure when grouped | |
| `bar` | `by` 1-2; one measure with 2 `by` | bucket |
| `table` | measures; `by` ≤ 3 | bucket |
| `stat`, `gauge` | exactly 1 measure; gauge: `requires min_max` | bucket, `by` |
| `heatmap` | `histogram(…)`, bucket | `by` |
| `histogram` | `histogram(…)` | bucket; `by` ≤ 1 |
| `scatter` | 2 measures, `by` 1-2 | |
| `state_timeline` | 1 measure, `by` 1, bucket; `requires thresholds` | sort |

**Traces.** The `traces` viz does not list matching spans: candidates are `(namespace, trace_id)` pairs with at least one span matching the filters in the window, ranked by the matching spans' `max(duration_ms)` (or `min(start_time)` for `sort +start`), limited to `limit` (at most 1,001); each candidate returns one row, the trace's root span (or its earliest span in the window when the root is outside it), with `status` set to error if any span in the trace errored. The reference says exactly this; the Explore trace list labels columns "root service" and "root duration". Trace sort keys: `duration` (canonical for `duration_ms`), `errors`, `+start`; `sort +duration` and `sort start` are `traces sort descending by duration, by errors, or ascending by start only`. Logs: `time`, `+time`. `log_patterns`: `count`.

**`sort errors` stays.** It is valid stored state (appends `status = 'STATUS_CODE_ERROR'` and orders by duration). FanQL accepts and prints it with the notice `sort errors also filters to error spans; new queries: write status:error … sort duration`; completion after `sort` under traces offers `duration`, `+start` and `errors` (detail "filters to errors, orders by duration"); the reference teaches `status:error … sort duration` and lists `errors` as the legacy combined key.

**Bucketed sort and limit.** For bucketed aggregates the compiler orders by time and selects series by `options.top`, ignoring `Sort`/`Limit`; the panel accepts them, so FanQL must too. With `viz` in a bucketed family, a `sort` or `limit` yields the notice `sort does not apply to bucketed series; series are chosen by top (options)`; a `limit` on `stat`/`gauge` instead reports `limit does not apply to stat panels; the whole window produces one value` (using the selected visualization name), at path `limit`. Without a viz, no notice.

**Error model.** (1) A text with any problem has no executable form: the parse response carries no `query`, Run and Save stay disabled, the user's text is never replaced. (2) All problems in one pass, in source order, capped at 20 (`more problems follow; fix these first` as the 21st); recovery exists to find more problems and to drive completion, never to produce a runnable remainder. (3) A repaired preview is returned only when requested (`preview: true`) and is labelled `not executable`. (4) Notices are a separate list and never block. (5) Fix-its are suggestions attached to a problem; applying one is a user action.

```go
type Problem struct {
	Path    string    `json:"path"`
	Message string    `json:"message"`
	Hint    string    `json:"hint,omitempty"`
	Span    *TextSpan `json:"span,omitempty"`    // additive; code-point offsets, end exclusive
	FixIts  []FixIt   `json:"fix_its,omitempty"` // additive; alternatives, never applied automatically
}
type TextSpan struct{ Start, End int }
type FixIt struct {
	Label string   `json:"label"`
	Span  TextSpan `json:"span"`
	Text  string   `json:"text"`
}
```

`panel.Problem` keeps `Path`, `Message`, `Hint`; `Span` and `FixIts` are additive. There is no severity field: notices (`{path, message, span?}`) use their own list.

#### 0.13 API, envelope, compatibility, bounds

```
POST /api/fanql/parse
  {text, source?, viz?, variables?, preview?: bool}
  200 {query, canonical, viz: {requested?, compatible: [{viz, requires: []}], edits: [{viz, text}]},
       notices: [{path, message, span?}]}
  400 {code: "invalid_fanql", message: "The query has problems.", problems: [...], preview?: {text, note: "not executable"}}

POST /api/fanql/format
  {query, viz?, variables?, panel?}
  200 {text, exactness: "exact" | "normalized", editable: bool, reason?: string, requires: [{name, satisfied}], notices: [...]}
  400 {code: "invalid_spec", ... problems}           (the query fails QueryShape under every viz)
  503 {code: "unavailable", message}                 (engine parse failed operationally)

POST /api/fanql/complete
  {text, cursor, source?, viz?, window?, from?, to?, namespace?, variables?}
  200 {replace: {start, end}, items: [{label, insert, kind, detail}],
       schema: {window, sampled: true, warm: bool, recent: bool}}

GET  /api/fanql/reference        text/markdown
```

`format` needs in-memory shape validity under some viz, the supplied `variables` to judge `$x` references (without them, references are notices), and the engine only for `ParseSQL`/`RenderSQL` of stored entries; it never runs `Check`'s bind step or `Normalize`; `panel` is accepted so a builder round trip reports `requires: [{name, satisfied}]`; gauge needs `min_max`, and a state timeline needs `thresholds`. Other panel configuration is ignored.

**Handler.** `PanelHandler` holds a `PanelEngine` (`Exemplars`, `Run`, `ResolveVariables`, `Schema`) and no parser, so FanQL gets its own handler with an injected `panel.Parser` (`*query.Duck` implements it):

```go
type FanQLHandler struct {
	parser panel.Parser
	engine PanelEngine // Schema, for completion
}
func RegisterFanQLRoutes(e *echo.Echo, parser panel.Parser, engine PanelEngine)
```

Routes use the 20-second panel deadline; operational errors (`panel.isOperational`) become `503 unavailable` and are logged through `PresentError`, never turned into problems; bodies are decoded with `decodeStrict` and the existing 512 KiB limit; all routes need the `telemetry:read` capability and are classified in `classifyRoute`.

**Envelope.** Panels store `panel.Query` only; the text is derived. Everything else that carries text carries the context the text needs:

```json
{
  "text": "spans where service:$service | p95(duration) by http_route every 1m",
  "editable": true,
  "query": { "...": "the lowered Query; required when editable is false, optional otherwise" },
  "viz": "timeseries",
  "time": { "range": "1h" },
  "compare": "previous_period",
  "variables": [ { "name": "service", "kind": "query", "from": "spans", "field": "service", "include_all": true } ],
  "vars": { "service": "checkout" },
  "namespace": "prod",
  "options": { "top": 6, "style": "line" }
}
```

When `editable` is false the envelope must carry `query`; consumers open the builder on it, show `text` read-only and never submit that text to `parse`. Explore URL: `?q=<text>&viz=&range=1h|from=..&to=..&compare=&vars=<json>&ns=`; an oversized text falls back to a saved view carrying `query`. `time` is `panel.Time`; `compare` is `previous_period` (the only value); `namespace` scopes schema and completion and is not a query filter (`namespace:prod` is). MCP `query_telemetry` gains optional `text` beside `panel`; `panel.query` stays the documented first choice for agents, with SQL for complex work; the text form is accepted and documented for agents but never required. Chat transcripts and documentation carry text without an envelope; they are not machine inputs.

**Compatibility.** FanQL has no language-version field or negotiation. Canonical output is pinned by a corpus, and registry tests detect changes to accepted text meanings. The structured query evolves under `panel.SpecVersion` (1); the measure grammar is additive to the spec.

**Bounds.** Interactive (the text): 4,000 code points **and** 8,192 bytes, whichever fails first (`a query is at most 4000 characters (8192 bytes)`); 1,024 tokens; 512 AST nodes; nesting depth 16 (groups, negation, calls, arithmetic together); `in` lists of 100 values; 20 problems; 50 completion items. Structured (the `Query`), enforced at lowering and reported at the text span: 16 entries (at the 17th entry's span); 500 **bytes** per lowered entry (`this filter lowers to N bytes; filters are at most 500`); 6 measures; 3 `by` fields and the viz's maximum; the expression budgets of 0.10. UI spans are code points; the UI converts once with `Array.from(text)`, and a test covers astral characters in values and keys.

#### 0.14 Completion

Inputs: `GET /api/telemetry/schema` returns low-cardinality column values (12 per column, 200 for `service`), attribute keys sampled from 5,000 rows with at most 200 keys per signal (each with type, count and up to three service names), metric names; windows are limited to 24h and relative to now, cached one minute per `(window, namespace)`. Rules that follow:

1. **Window clamp and recency, disclosed.** Completion requests `window = min(explore range, 24h)`. `schema.recent` is true whenever the Explore window is absolute or longer than 24h, and the popup header then says "keys seen in the last 24h (recent), not in the selected interval".
2. **Sampled metadata ranks; it never hides.** Every key seen is offered; keys not seen are still legal. Service lists only boost keys whose list contains a `service:` literal already in the text.
3. **Cold cache never blocks, and never stampedes.** With no schema for the cache key, completion answers from registries alone with `schema.warm: false` and requests one coalesced background fetch per `(window, namespace)` key, at most 4 concurrent process-wide, on a detached context with the existing 20-second schema deadline. A failed fetch is cached negatively for 10 s and reported as `warm: false, error: true`; the UI re-requests on the next keystroke and shows "schema unavailable" after two failures.
4. **Types rank, not gate.** In a numeric argument slot, number-typed attributes list first, then everything else. `count_distinct` lists every field. `quantile`'s second slot offers `0.5 0.9 0.95 0.99`. Zero-argument functions insert `()` and move on.

Algorithm: lex the whole text (never fails); the current token is the one containing the cursor or ending at it, and `replace` is its whole span (`sev|erity:error` replaces `severity`). Inside a `String` or `Raw` token: no completion. Inside a `BracketKey` token (`attributes['htt|`): key completion for that scope, the replace span is the key text inside the quotes and the insert closes the bracket (`http.target']`); an unterminated bracket key is still a `BracketKey`. Parse the prefix in completion mode, recording the set of alternatives the parser would accept next (`source`, `clause`, `field`, `operator`, `value(field)`, `measure`, `marg(func, index)`, `bucket`, `sort_key`, `integer`, `variable`, `bracket_key(scope)`; the term-start class is the union of field, free text, `-`, `(`, `$`, `not` and clause words). Map classes to candidates ranked by exact prefix match, substring match, class order and schema count; at most 50 items; the viz and variables shape the sets:

| Position | Candidates |
|---|---|
| source | `spans logs metrics` |
| after source | `where`, `\|`, `sort`, `limit`; plus fields |
| term start | columns with descriptions; attribute keys (dotted bare; single-segment as `attributes['k']`; `resource.k`); `-`, `(`, `not`; declared `$vars`; a trailing "free text searches body/operation" item |
| after a field | `:`, `>`, `>=`, `<`, `<=`, `!=`, `~`, `in`, `not in`, `between`, `is null`, `like`; after a time field only comparisons; after `duration` comparisons and `between`; after `severity` comparisons (ranges) and `:` |
| after `field:` | the column's values; vocabulary aliases for `status kind severity type`; metric names; `$vars`; `duration`: `500ms 1s 5s`; boolean attributes: `true false`; `*` |
| after `severity>=` etc. | `trace debug info warn error fatal` |
| after `\|` | measures valid for the signal (`error_rate` hidden on metrics, `last` only on metrics), `histogram(` when the viz is heatmap/histogram or unset, `count_if(` |
| inside `fn(` | by signature and argument index |
| after a measure | `,`, `as`, `by`, `every` (only when the viz takes a bucket or is unset), `sort`, `limit`; `+ - * /` (not after `share()`) |
| after `by` | string columns and attribute keys; respects the viz's maximum |
| after `every` | `auto` and the twelve buckets |
| after `sort` | the stage's aliases with `+` forms; without a stage, the viz's row keys |
| after `limit` | `10 50 100 1000`, capped by the viz |

Latency acceptance: p95 under 30 ms end-to-end with a warm cache and under 5 ms for parse and rank alone; cold bursts measured separately (50 concurrent completions on an empty cache issue exactly one schema query per cache key and at most 4 at once).

#### 0.15 FanQL at a glance

One line per query. Prefix `-` excludes and includes rows missing the field; `!=` is SQL. Mix `and` and `or` only with parentheses. `$name` is a dashboard variable; a term mentioning a variable set to All is removed, so keep fixed conditions in their own terms.

```
 1  spans where kind:server | error_rate() by service every 1m
 2  spans where service:checkout duration>500ms sort duration limit 50
 3  spans where service:$service (status:error or status:unset) | count() by http_route
 4  spans where kind:server | count_if(http_status_code>=500) / count() * 100 as pct_5xx by http_route
 5  spans where kind:server | (count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex
 6  spans | histogram(duration) by service
 7  spans where service:checkout status:error -http.target:/healthz | count() by http_route
 8  spans where user.id:* | count_distinct(user.id) by service
 9  logs where service:api severity:error timeout sort time limit 200
10  logs where severity>=warn | count() by body_template sort count limit 10
11  metrics where name:http.server.requests | rate() by service every 1m
12  metrics where name:k8s.container.restarts | last(value) by resource.k8s.pod.name
```

| # | Shows | Viz | Notes |
|---|---|---|---|
| 1 | spans, built-in ratio, grouping, bucket | timeseries | percent of server spans in error; top 6 services, rest in Other |
| 2 | spans as traces, duration unit, sort, limit | traces | returns trace roots ranked by the slowest matching span |
| 3 | variable, mandatory `or` grouping | bar/table | two entries; under All the status group stays |
| 4 | conditional aggregate, arithmetic, alias | table/bar | numeric comparison on the text column via TRY_CAST; no bucket as written, so `timeseries` is offered only as an edit that appends `every auto` |
| 5 | Apdex with disjoint predicates | stat | a span at exactly 500 ms counts once |
| 6 | distribution | histogram | `every 1m` instead of `by` makes it a heatmap |
| 7 | null-inclusive exclusion | bar/table | spans without `http.target` are counted |
| 8 | existence, string distinct count | table | `-user.id:*` would find spans missing the key; `count_distinct(user.id)` lexes as one `Path` argument |
| 9 | logs, free text, severity alias | logs | `timeout` is a case-insensitive substring of `body` |
| 10 | severity range, pattern grouping | log_patterns | `>=warn` is WARN, ERROR, FATAL on the stored band |
| 11 | metrics counter rate | timeseries | counter increase per second, not points per second |
| 12 | metrics gauge, resource attribute | table | `last(value)` is kind-aware; `resource.k` is `resource['k']` |

Comparison claims are descriptive only: over the four reference queries whose output contract is equivalent across FanQL, Fanout SQL, SPL, KQL and Grafana, the FanQL strings total 255 characters against 285 (SPL), 324 (Grafana, with two UI settings), 460 (KQL) and 670 (SQL). These are string sums for typing on a stated dataset, not claims about task completion, learning time or error rates.

#### 0.16 Scope: preview and later

The preview covers, as corpus fixtures that parse, lower, pass `Check` against the pinned engine and run on seed data: error rate by service; p99 by endpoint; top-N slow operations; Apdex; logs for a trace; count distinct users; latency histograms (spans and metrics); 4xx/5xx classes via `count_if`; slow DB statements; queue lag gauges; pod restarts; counter rates; per-route error ratios; errors, total and ratio in one table; absence of a field through `-k:*`; save, share and rerun through the envelope. Partial in the preview: comparison with the previous period (envelope `compare`, not text); logs around a trace's time (typed timestamps by hand).

| Later capability | Why deferred | Where it goes |
|---|---|---|
| Derived grouping (`4xx`/`5xx` classes in `by`) | needs row expressions in `by`; the engine groups by catalog fields only | `eval` stage in a typed plan lowered to SQL |
| Multi-dimension timeseries | renderer and series selection are one-dimensional | engine and renderer |
| Correlation (`correlate … on trace_id`, a semijoin) and joins | no cross-signal execution path | plan lowered to SQL through the existing gate |
| Top-N per group, per bucket | the compiler folds into Other by one ranking | plan to SQL |
| Arbitrary-lag overlay (`compare 7d`) | `compare` is previous-period only | executor change plus envelope |
| Explicit time in text (`since`/`until`) | time lives in the dashboard/Explore state | envelope first; text later with the reserved words |
| `share()` inside arithmetic | denominator scope through `Binary` nodes in five paths | metadata on the measure expression |
| Absence operators | needs expected-entity inventories | alerts |
| Metrics arithmetic | series alignment across kinds | after the spans/logs expressions land |
| Alias references between measures | not in the structured query | later |

Extensibility by construction: the parser builds a tree and a separate lowering maps it to the structured query; `|` is a list of stages and any second aggregation stage is `FanQL runs one aggregation stage`, not a syntax error; the reserved words of 0.3 are errors now; functions, field spellings, vocabularies, buckets and foreign-word hints live in registries and the reference is generated from them with a test that every entry appears. Later plans store `Panel.SQL` for execution plus `Panel.Plan` for the round trip; the text round-trips from the plan, never from generated SQL, and editing a plan panel in the SQL tab detaches it. One engine, one gate, one renderer.

Acceptance replaces size and time estimates: the corpus (every fixture, the twelve examples, the covered workloads, at least sixty more texts); the round-trip guarantees of 0.11; DuckDB-backed truth tables for every negation row and leaf class, variables under All, and severity through ingestion; engine binding of every lowered shape; non-execution of texts with problems; fuzzing over lex, parse, resolve, lower and print in both lexer contexts plus random valid queries through `Format`; completion fixtures and the cold-cache test; ranking fixtures with the `Ranking` disclosure; measured latency (parse, resolve and lower p99 under 1 ms without the engine; `format` p99 under 20 ms for stored queries with up to 16 entries; `complete` p95 under 30 ms warm); every registry entry in the reference and `just docs-generate-check` guarding the site copy; the canonical freeze; registry growth detection; and the structural budgets on structured and text inputs alike.

### 1. Explore page (`/explore`)

A single page for ad-hoc questions:

- Signal picker (spans, logs, metrics), time range (the dashboard time control), and two editors on tabs:
  - **Query:** a one-line FanQL box (section 0) with completion from `POST /api/fanql/complete`; the default. Problems render at their spans with fix-its; notices render separately; the canonical text is shown after a run.
  - **Builder:** dropdowns for `measures`, `by`, `where` and `bucket`, fed by `GET /api/telemetry/schema` (columns, attribute keys per service, metric names, measure functions). Produces the structured `query`; edits stay in sync with the text box through `POST /api/fanql/format`. When `format` returns `editable: false`, the text is read-only and the builder edits the structured query.
  - **SQL:** the existing SQL editor and diagnostics, for advanced use.
- A viz picker limited to `viz.compatible` from the parse response; a compatible-with-edit viz (a timeseries that needs `every auto`, listed under `viz.edits`) is offered as an edit the user accepts.
- Run sends a transient one-panel dashboard spec to `POST /api/panels/query`, as the SQL editor preview already does, and renders the result with `PanelCard`.
- "Open as SQL" converts a builder query to editable SQL when the backend can render it; the reverse direction is not offered.
- "Save to dashboard" picks an existing dashboard or creates a new one, then applies `add_panel`.
- The URL carries the FanQL envelope (section 0.13: `?v=1&q=…&viz=&range=…&vars=…&ns=`) so an Explore view can be shared and bookmarked; an oversized `editable: false` text falls back to a saved view carrying `query`.

### 2. Logs and Traces as Explore presets

Rather than separate renderers, the rail gets **Logs** and **Traces** entries that open Explore preset to `viz: logs` (sorted by time) and `viz: traces` (sorted by duration or errors). The search bar accepts FanQL `filters` only (section 0.1); the page supplies the source and viz. Filters (service, severity, status, duration, free-text body search on the redacted body) are also builder fields. Clicking a row uses the existing drill drawers.

### 3. Dashboards without chat

- "New dashboard": a name plus a first panel built in the Explore drawer (see Decisions 1), and "Duplicate dashboard" on any dashboard. Uses `POST /api/dashboards`.
- "Add panel" in edit mode opens the Explore editor in a drawer and applies `add_panel`.
- "Edit panel" opens the same editor for builder and SQL panels (the current SQL editor becomes its SQL tab).
- When chat is enabled, "Create with AI" remains next to "New dashboard".

### 4. Alerts page (`/alerts`)

- List rules with state and last fired time, and firing history from `/api/alerting/alerts`.
- Create, edit, enable/disable and delete rules on the existing API, with a form for name, service, namespace, `for_seconds`, cooldown, repeat interval and webhook fields.
- The expression field accepts CEL with inline validation from the API's error response and a few starter templates (error rate, p95 latency, throughput, log volume) matching the documented rule kinds.
- Role checks follow the existing capabilities (`read` to view, `manage` to change).

### 5. AI affordances off when chat is disabled

`agentAvailable` (already in `app-context.ts`) gates every AI control: chat rail, Ask buttons, Explain, Fix, and "Create with AI". The home route lands on the default dashboard instead of chat when chat is disabled. Settings shows how to enable chat and how to connect an MCP client.

## Decisions from review (Claude's calls; the owner can overturn them)

1. **No empty dashboards.** The spec keeps its 1–40 panel invariant (`internal/panel/validate.go`). "New dashboard" asks for a name, then opens the Explore editor in a drawer; the dashboard is created with that first panel. Removing the last panel stays refused.
2. **No template gallery yet.** Only `DefaultSpec()` exists and every owner already has it. Add a "Duplicate dashboard" action instead (new name required); templates become a later content task.
3. **AI keys stay env-only in Fanout.** Self-hosted: Settings explains `FANOUT_AI_PROVIDER` and the key variables, and how to connect an MCP client. Hosted fanout.run: the customer enters a key on the fanout.run account page, and the control plane renders it into the tenant's environment and redeploys (Onebox Cloud design). Fanout gains no secret storage.
4. **UI saves follow the MCP rule that panels must return data,** enforced in the UI: Save and Add panel are enabled only after a run returned rows without error. The HTTP API and the existing SQL editor keep their current behaviour.
5. **Variables.** The Add-panel drawer opened from a dashboard can reference that dashboard's variables (for example `$service`), and the FanQL editor and the builder's where-editor understand `$name` with the All rule of section 0.6. When saving from Explore into a dashboard that has a variable on the same field, the save dialog offers to replace the literal filter with the variable.

## Constraints

- Follow `AGENTS.md`: API naming and route conventions for any new endpoint, snake_case JSON, no legacy or fallback paths, one renderer.
- Web conventions in the repository's web style guide and patterns documents; Mantine components already used by the host app.
- Tests for each page (render, empty state, error state, role gating, chat-disabled gating) alongside the existing Vitest suites; Go tests for any backend change.

## Left to the builder

- Exact layout and component structure of the builder, within the existing design system.
- Whether "Open as SQL" needs a small backend endpoint that renders a structured query to SQL, or can reuse existing code; add one only if no existing path exists.
- Encoding details of the Explore URL beyond the envelope fields section 0.13 fixes.
- Ordering of the work into pull requests.
