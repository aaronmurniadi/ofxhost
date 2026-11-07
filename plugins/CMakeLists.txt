# Builds the bundled OpenFX plugins into one .ofx.bundle.
#
# This is the plugin side of the OpenFX split: the host links HostSupport, a plugin links
# Support. They share the headers but nothing else, and a plugin is loaded by the host at
# runtime rather than linked into it - which is why this is a MODULE library with a bundle
# around it and not an object file in the executable.
#
# Run from the repository root:
#   cmake -S internal/adapter/ofxhost/native -B build/ofxhost-native -DCMAKE_BUILD_TYPE=Release
#   cmake --build build/ofxhost-native -j
#
# The bundle lands beside the host library, and internal/adapter/ofxhost finds it there.

cmake_minimum_required(VERSION 3.20)
project(ofxhost_plugins LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Release)
endif()

find_package(Threads REQUIRED)

# The host project resolves the submodule from its own root; this file is included by the
# native build, so the same variable is already set. Fall back to a path relative to this
# file when it is configured on its own.
if(NOT DEFINED OFX_ROOT)
  set(OFX_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/../third_party/openfx")
endif()

include("${CMAKE_CURRENT_SOURCE_DIR}/../cmake/PatchOfxSupport.cmake")

set(BUNDLED_PLUGIN_SOURCES
  "${CMAKE_CURRENT_SOURCE_DIR}/Transform.cpp"
  "${CMAKE_CURRENT_SOURCE_DIR}/AutoExposure.cpp"
  "${CMAKE_CURRENT_SOURCE_DIR}/Denoise.cpp"
  "${CMAKE_CURRENT_SOURCE_DIR}/BundledPlugins.cpp"
)

add_library(OfxHostBundled MODULE ${BUNDLED_PLUGIN_SOURCES})
# The binary must carry the bundle's stem: the host derives the plugin binary from it
# (OfxHost.ofx.bundle -> Contents/<arch>/OfxHost.ofx).
set_target_properties(OfxHostBundled PROPERTIES
  PREFIX ""
  OUTPUT_NAME "OfxHost"
  SUFFIX ".ofx"
)
target_link_libraries(OfxHostBundled PRIVATE OfxSupport)
# The Support library references OpenGL for its own utilities even when a plugin does not.
if(APPLE)
  target_link_libraries(OfxHostBundled PRIVATE "-framework OpenGL")
else()
  find_package(OpenGL REQUIRED)
  target_link_libraries(OfxHostBundled PRIVATE OpenGL::GL)
endif()

if(APPLE)
  set(OFX_PLUGIN_ARCH "MacOS")
elseif(CMAKE_SYSTEM_PROCESSOR MATCHES "^(aarch64|arm64)$")
  set(OFX_PLUGIN_ARCH "Linux-arm-64")
else()
  set(OFX_PLUGIN_ARCH "Linux-x86-64")
endif()

set(OFX_BUNDLE "${CMAKE_CURRENT_BINARY_DIR}/OfxHost.ofx.bundle")
add_custom_command(TARGET OfxHostBundled POST_BUILD
  COMMAND ${CMAKE_COMMAND} -E make_directory "${OFX_BUNDLE}/Contents/${OFX_PLUGIN_ARCH}"
  COMMAND ${CMAKE_COMMAND} -E copy "$<TARGET_FILE:OfxHostBundled>"
    "${OFX_BUNDLE}/Contents/${OFX_PLUGIN_ARCH}/OfxHost.ofx"
  COMMAND ${CMAKE_COMMAND} -E copy_if_different
    "${CMAKE_CURRENT_SOURCE_DIR}/Info.plist" "${OFX_BUNDLE}/Contents/Info.plist"
  COMMENT "Assembling OfxHost.ofx.bundle"
)
