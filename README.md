# vtexkit

Go library for building command-line clients against [VTEX](https://vtex.com) storefronts —
the ecommerce platform behind a large share of Brazilian online retail.

It powers [`frescatto`](https://github.com/voska/frescatto) and
[`zonasul`](https://github.com/voska/zonasul). A store CLI built on it is a descriptor
plus a `main`.

## The idea: discover, don't declare

Almost everything that differs between VTEX stores is readable from the store's own
public API at runtime — which login methods it accepts, which payment systems, which
seller stocks a SKU, which delivery SLAs exist. A store descriptor should carry only
what genuinely cannot be discovered.

A complete descriptor for a stock VTEX store:

```go
var Store = store.Store{
    Name:        "frescatto",
    DisplayName: "Frescatto",
    BaseURL:     "https://www.frescatto.com",
}
```

```go
func main() {
    cli.Main(cli.App{Store: frescatto.Store, Version: version,
        Description: "Frescatto fish & seafood CLI."})
}
```

That yields a full CLI: auth, search, cart, lists, delivery, checkout, order history,
`doctor`, `schema`, and `exit-codes` — with `--json`, `--plain`, `--quiet`, `--select`,
and stable exit codes.

Stores that deviate declare only the deviation:

```go
var Store = store.Store{
    Name:     "example",
    BaseURL:  "https://www.example.com.br",
    MinOrder: money.Reais(100),            // business rule with no API field
    OAuth:    customDriver{},              // classic auth disabled at this store
    Quirks:   store.ClearSaleFingerprint,  // gateway needs a device fingerprint
}
```

## Packages

| Package | Responsibility |
|---|---|
| `store` | Store descriptor, quirks, OAuth driver interface, live capability probe |
| `vtex` | Client, auth strategies, search strategies, cart, delivery, checkout, orders |
| `cli` | The complete Kong command surface and `Main()` |
| `cli/outfmt` | human / json / plain / quiet / select / results-only |
| `cli/errfmt` | Stable exit codes and typed errors |
| `cli/config` | Per-store config under `~/.config/<store>/` |
| `money` | `Centavos`, an integer currency type |

## Design notes

**Auth is discovered, not declared.** `GET /api/vtexid/pub/authentication/start` reports
whether a store offers classic password login, emailed access codes, or OAuth providers.
The library picks a strategy from that. A store whose classic auth is disabled supplies a
`store.OAuthDriver`; everything else needs no code.

**Search avoids persisted GraphQL queries.** VTEX Intelligent Search is reachable over
REST, with the legacy catalog API as a fallback. The persisted-query path exists but is
opt-in: its SHA-256 hash rotates on every `search-graphql` release and fails by returning
nothing, which reads as "no results" rather than "broken".

**Money is integer centavos everywhere.** VTEX returns decimal reais from search and
integer centavos from checkout, and serializes the same field as `15372` in one API and
`26511.0` in another. `money.Centavos` normalizes at the boundary so no downstream code
does float arithmetic on prices.

**Sellers are per-item.** Which seller stocks a SKU is read from the catalog response,
never assumed.

**Carts self-heal.** VTEX snapshots profile, address, and payment data into an order form
when it is created and never refreshes it, so a cart created before an account had an
address can never complete a checkout. The library detects that and migrates the items to
a usable cart.

## Status

Used in production by two CLIs. The API is not yet stable; expect breaking changes before
a v1 tag.

## License

MIT
