# Volon — User Documentation Mini-Site

Static HTML documentation site for Volon, the multi-session repository automation system.

## Viewing locally

No build step is required. Open any `.html` file directly in a browser:

```bash
# Option 1: Open directly
open user-docs-mini-site/index.html

# Option 2: Serve with Python (avoids any relative-path quirks on some browsers)
python3 -m http.server 8080 --directory user-docs-mini-site
# Then open: http://localhost:8080

# Option 3: Serve with Node (if npx is available)
npx serve user-docs-mini-site
# Then open the URL shown in the terminal
```

## Structure

```
user-docs-mini-site/
├── index.html           # Home
├── about.html           # Philosophy, design principles, v0.5 milestone
├── setup.html           # Prerequisites, installation, first run
├── use-cases.html       # 6 concrete use case scenarios
├── examples.html        # Annotated workflow invocation traces
├── docs/
│   ├── index.html       # Docs landing — organized nav
│   ├── config.html      # agentrc.yaml configuration reference
│   ├── workflows.html   # Workflow contracts
│   ├── task-model.html  # Task model fields and lifecycle
│   ├── commands.html    # All slash commands reference
│   ├── artifact-classes.html  # Five artifact classes and intent field
│   ├── pcc.html         # Project Context Cache
│   └── orchestrator.html      # Orchestrator mode and loop protocol
├── assets/
│   ├── style.css        # Custom styles (complements Tailwind)
│   └── theme.js         # Light/dark mode toggle
└── README.md            # This file
```

## Tailwind CSS

The site uses **Tailwind CSS via CDN** (`cdn.tailwindcss.com`). No build step is needed for v1.

For production use, compile Tailwind CSS with the CLI to remove CDN overhead:

```bash
# Install Tailwind CLI
npm install -D tailwindcss

# Create tailwind.config.js
npx tailwindcss init

# Compile
npx tailwindcss -i assets/style.css -o assets/compiled.css --minify

# Then replace the CDN script tag in each HTML file with:
# <link rel="stylesheet" href="../assets/compiled.css">  (or "./assets/compiled.css" for root pages)
```

## Light/dark mode

The theme toggle button is in the top-right of the navigation bar. Theme preference is saved to `localStorage` under the key `volon-theme`. The default follows the OS preference (`prefers-color-scheme`).

## Content source

The mini-site presents content from the Volon system docs in `docs/*.md`. The HTML files are manually curated — they do not auto-sync from the Markdown sources. To update content, edit the relevant `.html` file directly.

## Notes

- No JavaScript frameworks — vanilla JS only for the theme toggle.
- No emojis used anywhere in the site.
- Mobile-responsive via Tailwind utility classes.
- All pages are self-contained (no server-side includes or templating).
