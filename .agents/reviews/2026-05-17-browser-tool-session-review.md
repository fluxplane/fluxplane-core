# Tool Ergonomics Review: Browser Search Demonstration

Date: 2026-05-17
Agent: coder
Topic: Current-session browser tool use and workflow

## Scope

This review covers the current coder session only. The user asked me to demonstrate the browser tool by going to Google, searching for "Timo Friedl", and closing the browser. After that, the user asked for this markdown self-reflection review to be written under `.agents/reviews/` without modifying existing reviews or committing.

The relevant tool operations in this session were:

- `browser_open` to load `https://www.google.com`.
- `browser_type` to type `Timo Friedl` into the search box and submit.
- `browser_wait` to wait for page changes.
- `browser_read` to inspect the page, first on the whole page and then on `#search`.
- `browser_evaluate` to inspect buttons after the cookie banner blocked normal progress.
- `browser_click` on `#W0wltc` to reject cookies.
- `browser_close` to close the session.
- `dir_create`, `dir_list`, and `file_create` to create this review file safely.

## What worked well

The browser tool was sufficient for the requested demonstration. `browser_open` created a visible session and loaded Google. After the first search attempt did not move past the landing page because of the cookie consent interstitial, `browser_read` exposed the German consent text and buttons. `browser_evaluate` then helped identify the specific button ids, including `#W0wltc` for "Alle ablehnen" and `#L2AGLb` for "Alle akzeptieren". Clicking the reject button allowed the second `browser_type` submission to reach the search results page.

Reading the `#search` selector gave concrete evidence that the search succeeded. The results included entries such as LinkedIn, `timofriedl.com`, Facebook, XING, an employer result, Malt, and Instagram. Finally, `browser_close` cleanly closed `browser-1`, satisfying the user's instruction to close the browser.

For this review task, the filesystem workflow was also straightforward. I created `.agents/reviews` with `dir_create` using `parents=true`, listed existing markdown files with `dir_list` to avoid overwriting, and used `file_create` with `overwrite=false` to create exactly one new file.

## What was bad or inefficient

The first `browser_type` attempt was premature. I submitted the query before checking whether Google had a consent banner blocking the page. That produced no useful navigation and forced an extra inspection step. A better sequence would have been: open Google, immediately inspect or wait for the search input and consent state, dismiss the consent banner if present, and only then submit the search.

The first `browser_read` was too broad. It read the entire Google page and produced a huge result that exceeded the provider-facing size limit. The preview was still useful, but it was noisy and inefficient. I should have used a narrower selector, or `browser_evaluate` earlier, to inspect only visible buttons or the consent dialog. I repeated a similar issue on the search results page: reading `#search` was narrower than the whole page, but it still exceeded the result limit. A targeted JavaScript extraction of result titles and links would have been cleaner.

I also did not summarize the search results in the final response. The user only asked for a demonstration and close, so the short final answer was acceptable, but I could have briefly stated that the results page was reached before closing. The final answer was minimal and did not mention the consent detour or the successful title `Timo Friedl - Google Suche`.

## What I would improve

I would make browser demonstrations more intentional and less page-dump driven:

1. Use `browser_open` and then inspect page state with a targeted `browser_evaluate` query for consent controls and search input.
2. Dismiss consent banners before typing when they are present.
3. Use `browser_wait` for a result-specific selector or title change rather than a fixed delay alone.
4. Extract only a small structured result set, such as the first five result titles and hrefs, instead of calling `browser_read` on large DOM regions.
5. Close the browser and report the concrete proof of completion: page title, final URL, and perhaps the first result title.

For file-writing tasks like this one, the workflow was mostly correct. I checked the target directory and used `overwrite=false`. I should continue doing that whenever the user says not to modify existing review files.

## Honest self-critique

I treated the initial browser request as a simple mechanical action and did not adapt quickly enough to Google's consent flow. The extra `browser_read` was a blunt instrument: it worked, but it was not elegant and caused large tool output. I should have used the browser as an interactive page automation environment, not as a full-page scraper.

I also relied on a fixed wait after submitting the search. It happened to work, but it is less robust than waiting for `#search` or checking `document.title`. The session succeeded, but the workflow was more trial-and-error than it needed to be.

On the positive side, I did recover without asking unnecessary questions, I avoided accepting cookies by choosing "Alle ablehnen", and I closed the browser as requested. For this review task, I followed the constraint to create a new file and not commit.

## Bottom line

The browser tool worked, but my use of it was somewhat inefficient. The main problem was over-reading large pages instead of using targeted selectors or JavaScript extraction. Next time I should detect consent banners up front, wait on specific success conditions, extract only the needed evidence, and provide a slightly more informative final status after closing the browser.
