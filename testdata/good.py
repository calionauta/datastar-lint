from datastar_py import SSE

def handler(sse: SSE):
    SSE.patch_elements(sse, "<div id='x'>x</div>", selector="#x")
    SSE.remove_element(sse, "#x")
