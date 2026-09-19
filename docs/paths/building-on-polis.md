# Reading path: building on polis

From the architecture to a second implementation. Each step says what it adds to the one before; the pages themselves hold the
detail. For the whole developer's door, see [the documentation's front page](../README.md#building-on-polis).

1. [Architecture](../general/concepts/architecture.md): the four surfaces, and which one you are building against.
2. [The graph](../signet/concepts/graph.md): **start here to build**: nodes, edges, and what a reader verifies.
3. [The recipe book](../signet/recipes/README.md), then recipes [1](../signet/recipes/01-make-a-claim.md), [4](../signet/recipes/04-list-attestations.md) and [13](../signet/recipes/13-verify-a-sites-attestations.md): make, list and verify claims, each run by the test suite.
4. [Content types](../general/concepts/content-types.md), [bundles](../general/concepts/bundles.md), [shapes](../general/concepts/shapes.md) and [themes](../general/concepts/themes.md): the content model and its presentation. [Writing a theme](../general/guides/themes.md) is the one task here with a guide.
5. [The content API](../api/developer/reference.md) and [the site API](../api/developer/site-api.md): the webapp's REST surfaces.
6. [The discovery service API](../ds/developer/api-reference.md), then [PQL](../general/reference/pql.md), then [PQL over HTTP](../ds/developer/pql-json-api.md): querying the network.
7. [JSON mode](../cli/user/json-mode.md): scripting the CLI.
8. [Snap-off architecture](../general/concepts/snap-off-architecture.md): replacing a layer.
9. **A second implementation:** [the Signet specifications](../signet/README.md), then [the signing base](../signet/spec/signing-base.md), [the policy grammar](../general/reference/policy-grammar.md) and [the content system](../general/concepts/content-system.md). For direct messages: the [at-rest format](../../cli-go/pkg/dm/FORMAT.md) and the [delivery protocol](../../cli-go/pkg/dm/PROTOCOL.md).
