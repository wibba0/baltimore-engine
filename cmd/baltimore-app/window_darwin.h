// Native macOS window hosting the dashboard in a WKWebView.
// RunApp never returns: it runs the Cocoa event loop until the user quits.
void RunApp(const char *url, const char *title);
