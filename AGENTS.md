# vtexkit

Go library for VTEX storefront CLIs. Powers `frescatto` and `zonasul`.

## Structure

```
store/      Store descriptor, quirks, OAuthDriver, capability probe
vtex/       Client, auth/search strategies, cart, delivery, checkout, orders
cli/        Kong command surface + Main()
cli/{outfmt,errfmt,config}/
money/      Centavos
```

## Build & test

`make build` `make test` `make lint` `make vet` `make ci`

## Principles

- **Discover, don't declare.** If a value is readable from the store's API at runtime,
  read it. The descriptor carries only what cannot be discovered.
- Data to stdout, hints and errors to stderr. Never mixed.
- Money is `money.Centavos` past the API boundary. No float prices in any signature.
- Exit codes are a published contract: 0 ok, 2 usage, 3 empty, 4 auth, 5 not found,
  6 forbidden, 7 rate limited, 8 retryable, 9 store rule, 10 config.
- Secrets live in the OS keyring only. Never in config files, never logged.

## Testing

Unit tests use `httptest`. Large recorded responses live in `vtex/testdata/`; small
fixtures stay inline. Fixtures must contain public product data only — no account,
address, or payment details.

## Commits

Conventional Commits (`feat:`, `fix:`, `chore:`, `docs:`, `test:`, `refactor:`).
