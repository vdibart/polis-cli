// Help, in kay's flyout (v5 spec 1.10): each section's fixed copy. What the
// page is for, each control by its label (part 3), where to go for the rest
// (ELSEWHERE), and Related topics. ⛔ Every word is fixed copy: no AI answers,
// and nothing is handed to an agent.
//
// Lifted from the v5 design's mockups, with three changes the spec made:
// "+new" is named by its accessible name (3.10, decision 6), the mockups'
// "ask kay" questions are Related topics (3.2), and links are /_/ addresses.
(function (root) {
  'use strict';
  var HELP = {
  "write": {
    "lead": "Everything you&rsquo;ve written lives here: your posts, and your comments on other people&rsquo;s.",
    "els": [
      [
        "<i>+</i>Write something new",
        "Starts something new. The editor opens in place, and <em>Esc</em> steps back out."
      ],
      [
        "posts · comments",
        "Filters. Pick one to see only that kind, or none to see everything."
      ],
      [
        "waiting",
        "Comments on your posts that need your blessing. Pick it to go through them."
      ],
      [
        "a card",
        "Click to read it in full, and again to fold it. <em>edit</em> opens it in the editor."
      ],
      [
        "comments under a post",
        "Click the count to show them. <em>Bless</em> lets a waiting comment appear on your site, <em>not here</em> declines it, and <em>unbless</em> takes one back."
      ],
      [
        "<i>↗</i>link out",
        "Opens the post on your site as readers see it. On one of your comments, it opens the post you replied to."
      ]
    ],
    "else": "To read what others write, go to <a href=\"/_/gather\">gather</a>. Whose comments appear without waiting is set in <a href=\"/_/enhance\">enhance</a> › people.",
    "topics": [
      [
        "What does blessing do?",
        "A blessing is your signed yes: this comment belongs beside your writing. Until then it lives only on its writer&rsquo;s own site. You can unbless it whenever you like."
      ],
      [
        "Can I change a post after publishing?",
        "Yes. Use <em>edit</em>, then publish again. It&rsquo;s signed again with your key, so readers can still tell it&rsquo;s yours."
      ]
    ]
  },
  "gather": {
    "lead": "What&rsquo;s happening around your polis network: posts from people you follow, replies to you, and who followed you.",
    "els": [
      [
        "<i><svg viewBox='0 0 24 24'><circle cx='11' cy='11' r='6.5'/><path d='M20 20l-4.2-4.2'/></svg></i>search",
        "Finds people and sites, and posts by a word of their title, across polis. Type an address like maren.polis.pub to go straight to that site."
      ],
      [
        "posts · comments · events",
        "Filters. Pick one or more to narrow the page, or none to see everything."
      ],
      [
        "new",
        "Shows only what arrived since you last looked. A red dot marks each new card."
      ],
      [
        "a post",
        "Click to read it in full. <em>reply</em> writes a comment under it, and <em>focus mode</em> opens the full editor."
      ],
      [
        "comments under a post",
        "Click the count to show them."
      ],
      [
        "<i>↗</i>link out",
        "Opens the post on its writer&rsquo;s own site."
      ],
      [
        "Follow · Follow back",
        "Adds someone&rsquo;s posts to this page."
      ]
    ],
    "else": "Your own posts and comments are in <a href=\"/_/write\">write</a>. To talk to someone privately, use <a href=\"/_/message\">message</a>.",
    "topics": [
      [
        "Why is nothing ranked?",
        "gather shows things newest first, from people you chose. Nothing is boosted, scored or hidden by an algorithm."
      ],
      [
        "Where does my reply go?",
        "On polis your reply lives on your own site, signed by you. It shows under the post once its writer blesses it."
      ]
    ]
  },
  "message": {
    "lead": "Private conversations, one card for each person you&rsquo;re talking to.",
    "els": [
      [
        "<i>+</i>Start a conversation",
        "Starts a conversation. You can message anyone you follow who follows you back."
      ],
      [
        "conversations · unread",
        "Filters. <em>unread</em> shows only conversations with something you haven&rsquo;t read yet."
      ],
      [
        "a conversation",
        "Its latest message shows on the card. Click to open the whole thing, which marks it read."
      ],
      [
        "Send",
        "Sends your message. <em>Ctrl+Enter</em> (or <em>⌘+Enter</em>) sends too, and <em>close</em> or <em>Esc</em> folds it away."
      ]
    ],
    "else": "How your messages are locked is in <a href=\"/_/enhance\">enhance</a> › people. To follow someone so you can message them, find them in <a href=\"/_/gather\">gather</a>.",
    "topics": [
      [
        "Who can read my messages?",
        "Only you and the person you&rsquo;re writing to. Messages are locked with your password before they leave your browser."
      ],
      [
        "Why can&rsquo;t I message someone?",
        "You both need to follow each other. It keeps strangers out of your messages."
      ]
    ]
  },
  "enhance": {
    "lead": "Your settings, in five groups. Click a card to open its settings, and again to close it.",
    "els": [
      [
        "<i><svg viewBox='0 0 24 24'><circle cx='11' cy='11' r='6.5'/><path d='M20 20l-4.2-4.2'/></svg></i>search",
        "Finds a setting by name."
      ],
      [
        "you · your site · people · agents · your data",
        "Filters. Pick a group to see only it, or none to see them all."
      ],
      [
        "You",
        "How you sign in: your email, and where you&rsquo;re signed in."
      ],
      [
        "Your site",
        "Your address, your bio, your theme, and the terms your writing carries."
      ],
      [
        "People",
        "Whose comments appear without waiting, and how your messages are locked."
      ],
      [
        "Agents",
        "Rosie, and any AI agent you connect, and what each one may do."
      ],
      [
        "Your data",
        "Your key, and taking everything with you."
      ]
    ],
    "else": "Back to your writing in <a href=\"/_/write\">write</a>.",
    "topics": [
      [
        "Who is Rosie?",
        "Rosie answers comment requests on your posts using your own rules. She signs with your key and marks everything she does. Enhance › agents shows whether she is on."
      ],
      [
        "Can I take my site elsewhere?",
        "Yes. Your data has everything, including your key, so your site and your writing can move to another host."
      ]
    ]
  },
  // "how it works" in the footer: kay's orientation to polis itself, the same
  // on every page (frame.js opens it with openHelp('polis')).
  "polis": {
    "lead": "polis is your own website for writing, and a way to read and answer what other people write. Everything you publish is signed with your key, so anyone can check it came from you.",
    "els": [
      [
        "<a href=\"/_/gather\">gather</a>",
        "What the people you follow are writing, and a search across all of polis."
      ],
      [
        "<a href=\"/_/write\">write</a>",
        "Your posts and your comments. When someone comments on your post, it waits here for your blessing."
      ],
      [
        "<a href=\"/_/message\">message</a>",
        "Private conversations with one other person, locked before they leave your browser."
      ],
      [
        "<a href=\"/_/enhance\">enhance</a>",
        "How your site looks, whose comments appear, the agents you connect, and taking everything with you."
      ]
    ],
    "else": "Curious how it works underneath? The whole design is written up for developers <a href=\"https://github.com/vdibart/polis-cli/blob/main/docs/README.md\" target=\"_blank\" rel=\"noopener\">on GitHub</a>.",
    "topics": [
      [
        "What does signed mean?",
        "Your site holds a key only you control. Each post and comment carries a signature made with it, so a copy anywhere can be checked against your site, and nobody can put words in your name."
      ],
      [
        "Who owns my site?",
        "You do. Your writing lives on your own site, and enhance › your data lets you take all of it, key included, somewhere else."
      ],
      [
        "Can I use my own AI?",
        "Yes. Connect it in enhance › agents. Whatever it writes for you is marked as written by an agent, under the permission you gave it, and you can switch it off whenever you like."
      ]
    ]
  }
};
  root.PolisV5Help = HELP;
  if (typeof module !== 'undefined' && module.exports) module.exports = HELP;
})(typeof window !== 'undefined' ? window : globalThis);
