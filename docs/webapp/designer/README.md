# Polis Design System

This directory contains the design system documentation for the polis.pub unified UI. It captures brand identity, theming architecture, navigation patterns, page structures, component specs, and the reasoning behind key design decisions.

## Documents

| Document | Purpose |
|----------|---------|
| [brand.md](brand.md) | Brand identity: logo, typography, color, tone |
| [theme-system.md](theme-system.md) | How theming works across system, user, and foreign contexts |
| [navigation.md](navigation.md) | Icon nav bar architecture, avatar menu, autohide behavior |
| [pages.md](pages.md) | Page states, routing model, content panel specs |
| [components.md](components.md) | Reusable UI patterns and elements |
| [decisions.md](decisions.md) | Design decision log with reasoning |

## Mockups

Interactive HTML mockups live in `docs/design/v3/mockups/unified-ui/`:

| Mockup | What it shows |
|--------|---------------|
| `themed-flow.html` | **Primary reference.** Full 5-page interactive flow with VICE (vdibart) and Especial-Light (alice) themes |
| `landing-hybrid.html` | "Show me then tell me" landing page (SOLS system theme) |
| `theme-adaptive-nav.html` | Nav compatibility stress test across all 7 theme combinations |
| `icon-nav-concept.html` | Earlier iteration with neutral theme (superseded by themed-flow) |

## How to Use

- **Implementing a new page/view**: Start with [pages.md](pages.md) for the page architecture, then [components.md](components.md) for the building blocks, then [navigation.md](navigation.md) for how the nav contextualizes.
- **Adding a new theme**: See [theme-system.md](theme-system.md) for the variable contract every theme must satisfy.
- **Making a design decision**: Check [decisions.md](decisions.md) first to ensure consistency with established patterns. Add new decisions there.
- **Brand questions**: See [brand.md](brand.md) for logo usage, typography, and tone.
