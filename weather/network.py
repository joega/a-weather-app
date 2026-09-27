"""Network-only helpers, imported when an explicit fetch is attempted."""
from urllib.request import HTTPRedirectHandler


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("weather provider redirect refused")
