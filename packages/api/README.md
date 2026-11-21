> This package is part of Actual Pocket, Danilo Urrutia's personal fork of [Actual Budget](https://github.com/actualbudget/actual). Pull requests will most likely not be merged, and issues may or may not be fixed. See the [fork policy](../../CONTRIBUTING.md). Upstream package downloads and community links refer to the original project.

```
npm install @actual-app/api
```

View docs here: https://actualbudget.org/docs/api/

## TypeScript

`@actual-app/api` publishes TypeScript declarations. Consumers using TypeScript must set `moduleResolution` to `"bundler"`, `"nodenext"`, or `"node16"` in their `tsconfig.json`. Legacy `"node"` / `"node10"` / `"classic"` resolution is not supported in strict mode — the published declarations rely on package.json `exports` conditions that older resolvers don't honor.
