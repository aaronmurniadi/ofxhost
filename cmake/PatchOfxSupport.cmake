# Builds the OpenFX Support library, which is what a plugin links against.
#
# The host only needs HostSupport; the bundled plugins need this one, and the two are
# different libraries that happen to live in the same submodule.
#
# One change from upstream. The support library's log-indent counter is a process-global int
# (upstream's own comment calls it "not MP sane"), and action dispatches from the render and
# the UI thread race on it. A private copy is patched to thread_local so the submodule stays
# pristine. The anchor check keeps the patch honest: if upstream renames the line, configuring
# fails instead of silently building the racy version.

# OFX_ROOT rather than CMAKE_SOURCE_DIR: this file is included from a subdirectory whose
# source dir is not the repository root, so the root would resolve to the wrong place.
if(NOT DEFINED OFX_ROOT)
  set(OFX_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/third_party/openfx")
endif()
set(OFX_SUPPORT_SRC "${OFX_ROOT}")
set(OFX_SUPPORT_PATCHED_LOG "${CMAKE_BINARY_DIR}/ofxsLog.cpp")

file(READ "${OFX_SUPPORT_SRC}/Support/Library/ofxsLog.cpp" _ofxs_log)
string(REPLACE
  "static int gIndent = 0;"
  "static thread_local int gIndent = 0;"
  _ofxs_log "${_ofxs_log}")
if(NOT _ofxs_log MATCHES "static thread_local int gIndent = 0;")
  message(FATAL_ERROR "ofxsLog.cpp gIndent patch anchor not found; update cmake/PatchOfxSupport.cmake")
endif()
file(WRITE "${OFX_SUPPORT_PATCHED_LOG}" "${_ofxs_log}")

add_library(OfxSupport STATIC
  "${OFX_SUPPORT_SRC}/Support/Library/ofxsCore.cpp"
  "${OFX_SUPPORT_SRC}/Support/Library/ofxsImageEffect.cpp"
  "${OFX_SUPPORT_SRC}/Support/Library/ofxsInteract.cpp"
  "${OFX_SUPPORT_PATCHED_LOG}"
  "${OFX_SUPPORT_SRC}/Support/Library/ofxsMultiThread.cpp"
  "${OFX_SUPPORT_SRC}/Support/Library/ofxsParams.cpp"
  "${OFX_SUPPORT_SRC}/Support/Library/ofxsProperty.cpp"
  "${OFX_SUPPORT_SRC}/Support/Library/ofxsPropertyValidation.cpp"
)
target_include_directories(OfxSupport PUBLIC
  "${OFX_SUPPORT_SRC}/include"
  "${OFX_SUPPORT_SRC}/Support/include"
)
target_link_libraries(OfxSupport PUBLIC Threads::Threads)
set_target_properties(OfxSupport PROPERTIES POSITION_INDEPENDENT_CODE ON)
