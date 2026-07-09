from datastar_py import SSE

def handler(sse: SSE):
    SSE.patch_elements(sse, "<div></div>")
    SSE.patch_elements(sse, "<div></div>", selector="")
    SSE.remove_element(sse, "")
