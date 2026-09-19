# Policy and licence

*Who may reach me, against what may be done with my work*

*For* [Writers](../../README.md#writing-on-polis) · [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *About* [Content](../README.md#content) · [Relationships](../README.md#relationships) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../reference/policy-grammar.md) · [spec](../../signet/spec/license.md) · [guide](../../cli/user/policies.md) · [guide](../../signet/guides/set-your-terms.md)

A polis site can say two kinds of thing about how the world may treat it. They sound alike, and both get
called "my rules". **They point in opposite directions, speak to different readers, use different words,
and live in different files.** This page explains the split. The rules themselves are specified
elsewhere, and linked below.

| | **Policy** | **Licence** |
|---|---|---|
| The question | *who may reach me, and what do I accept?* | *what may be done with work I made?* |
| Direction | **inbound**: others → me | **outbound**: me → the world |
| Who reads it | polis software: yours and your peers'; a discovery service applies only its own operator's rules | any consumer, mostly not polis: crawlers, AI builders, archives, people |
| Vocabulary | polis's own decision verbs ([the grammar](../reference/policy-grammar.md#tldr)) | other people's standards, IETF AIPREF and RSL ([vocabulary](../../signet/spec/license.md#3-vocabulary)) |
| Where it lives | rules files, **not signed** | a licence document and each work's frontmatter, **signed with your key** |
| What makes it matter | software applies it | nothing applies it: it is a **statement** |

## They share a noun and nothing else

**A policy is about interaction.** It decides what happens when something arrives or is announced about
you: a direct message from a stranger, a comment on your post asking to be blessed. Its readers are
programs that have to reach a decision, so its words are decisions. See the
[grammar](../reference/policy-grammar.md) for the verbs, and the [policies guide](../../cli/user/policies.md) for
writing rules.

**A licence is about use.** It says what a third party may do with your work once they have a copy:
train a model on it, index it for search, quote it into an answer, credit you. Its readers mostly have
never heard of polis, so polis invents none of the words. It carries standard vocabularies and adds only
the binding to your key ([why](../../signet/concepts/assertions.md#carry-the-standards-own-the-binding)).

Nothing converts one into the other. A rule that denies every comment says nothing about AI training.
A licence that reserves every right says nothing about who may comment or send you a message. **Setting
one never sets the other**, and a reader that infers one from the other is wrong both ways.

## Who applies what

**Your inbound rules are applied by your own software.** Whether to accept a message is decided when it is
delivered to you. Whether a comment is blessed is decided by the post author's own site, never by a
discovery service. A discovery service has [rules of its own](../reference/policy-grammar.md#layer-3--ds-operator-ingestion),
written by whoever runs it, about what it admits into its stream. Those are the operator's rules, not
yours. It decides no blessing for anyone.

**A licence is applied by nobody.** It is not enforcement: a signature stops no crawler
([§1 of the spec](../../signet/spec/license.md#1-what-problem-the-signature-solves)). What signing buys is that
the terms are legible, dated, provably yours, and travel inside each work. polis turns them into the
surfaces crawlers already read (`robots.txt`, `rsl.xml`, page headers), generated from the signed source
every time. Whether anyone honours them is outside polis.

So the two fail differently. A missing or broken policy changes what your site does. A missing licence
changes nothing your site does. It means you have stated no terms, which is a defined state
(*unknown*), not a gap to be filled.

## Why one is signed and the other is not

A licence is **evidence about you** for readers far away and years later: *what did the author say about
this work, and when?* That question only has an answer if the statement is bound to your key and cannot
be edited afterwards. Terms are also **not retroactive**: a work carries the terms it was published
under, and a newer licence never reaches back into it ([two artifacts, two questions](../../signet/spec/license.md#51-two-artifacts-two-questions)).

A policy is **configuration for your own software**, read now and changed whenever you like. Your public
rules file is published so peers can see your stance before they try, but the decision that counts is the
one your software makes when something arrives. A signature would prove what your rules said, not what
your software did with them.

## Common confusions

- **"My policy allows comments, so my work is open."** No. Comment rules govern interaction on your site.
  Use of your words elsewhere is the licence's question, and without a licence the answer is *unstated*.
- **"Reserved terms will block comments or messages."** No. A licence has no inbound meaning.
- **The reserved outbound verbs in the grammar.** The grammar has a second, outbound layer, reserved and
  not yet applied. It would decide whether your site *announces* its own events to a discovery service.
  That is routing among polis peers, not terms of use, and it is not a licence either.
- **"My host set my terms."** Nothing states terms for you. No host, no background job, no signup form, and
  no default ([the rule](../../signet/spec/license.md#nobody-states-terms-for-the-author)). A hosted
  service that switches on a user agent for you issues a *grant* under its hosting terms. A grant lets the
  agent apply your own rules, signed with your key and marked as the agent's. **A grant is not
  terms**, and never counts as your consent to anything
  ([delegation §4](../../signet/spec/delegation.md#4-consent-defaults-and-versioning)).
- **"Following someone is a policy about them."** A follow is a signed statement *about your own network*.
  A rule may refer to it (for example, accept messages from people you follow), but the follow file is
  neither a rule nor a licence.

## Where to go next

| To | Read |
|---|---|
| write or change the rules for comments and messages | [Policies](../../cli/user/policies.md) |
| look up a verb, a layer or the evaluation order | [Policy grammar](../reference/policy-grammar.md) |
| state, change or withdraw your terms | [Set your terms](../../signet/guides/set-your-terms.md) |
| implement or verify a licence | [The licence spec](../../signet/spec/license.md) |
| see a licence beside polis's other signed claims | [Assertions](../../signet/concepts/assertions.md#licence-here-are-my-terms) |
