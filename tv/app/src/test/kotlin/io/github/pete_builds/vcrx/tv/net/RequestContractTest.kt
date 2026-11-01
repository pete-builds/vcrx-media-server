package io.github.pete_builds.vcrx.tv.net

import java.io.File
import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.KSerializer
import kotlinx.serialization.descriptors.PolymorphicKind
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.descriptors.SerialKind
import kotlinx.serialization.descriptors.StructureKind
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.yaml.snakeyaml.Yaml

/**
 * Every request body the TV sends against its schema in server/api/openapi.yaml.
 * The server rejects a field it does not know with 400, and the TV's encoder sends
 * every field, defaults included, so one new field in a request class would break
 * that request against any server that has not learned it yet. This fails on any
 * property the schema does not define, nested objects and list items included.
 *
 * Gradle passes the spec's path in vcrx.openapi (app/build.gradle.kts); the Docker
 * build copies the spec to the same place relative to tv/ (tv/Dockerfile).
 */
@OptIn(ExperimentalSerializationApi::class)
class RequestContractTest {
    /** What a request carries: the operation in openapi.yaml and the class ServerApi encodes for its body. */
    private data class Body(val method: String, val path: String, val serializer: KSerializer<*>)

    private val bodies = listOf(
        Body("post", "/api/v1/auth/login", LoginRequest.serializer()),
        Body("post", "/api/v1/auth/auto", AutoLoginRequest.serializer()),
        Body("post", "/api/v1/auth/switch", SwitchRequest.serializer()),
        Body("post", "/api/v1/pair", PairStartRequest.serializer()),
        Body("post", "/api/v1/pair/poll", PairPollRequest.serializer()),
        Body("post", "/api/v1/invites/redeem", RedeemRequest.serializer()),
        Body("put", "/api/v1/layout/home", HomeLayoutInput.serializer()),
        Body("post", "/api/v1/admin/activity/sessions/{session_id}/terminate", TerminateRequest.serializer()),
        Body("put", "/api/v1/items/{item_id}/watched", SetWatchedRequest.serializer()),
        Body("put", "/api/v1/me/playback-settings", PlaybackSettings.serializer()),
        Body("put", "/api/v1/me/playback-settings", BeforeMovieSettings.serializer()),
        Body("post", "/api/v1/playback/sessions", PlaybackRequest.serializer()),
        Body("post", "/api/v1/playback/sessions/{session_id}/events", PlaybackEvent.serializer()),
        Body("post", "/api/v1/music/queue", MusicQueueRequest.serializer()),
        Body("post", "/api/v1/music/tracks/{track_id}/sessions", MusicListenRequest.serializer()),
    )

    private val spec: Map<*, *> by lazy {
        val path = System.getProperty("vcrx.openapi")
            ?: throw AssertionError("vcrx.openapi is not set; run the tests through Gradle (app/build.gradle.kts sets it)")
        val file = File(path)
        assertTrue("openapi.yaml not found at $path", file.isFile)
        file.reader().use { Yaml().load<Map<*, *>>(it) }
    }

    @Test
    fun everyRequestBodyIsInTheContract() {
        val problems = mutableListOf<String>()
        for (body in bodies) {
            val operation = (spec["paths"] as Map<*, *>)[body.path]?.let { (it as Map<*, *>)[body.method] } as Map<*, *>?
            if (operation == null) {
                problems += "${body.method.uppercase()} ${body.path} is not in openapi.yaml"
                continue
            }
            val schema = operation.at("requestBody", "content", "application/json", "schema") as Map<*, *>?
            if (schema == null) {
                problems += "${body.method.uppercase()} ${body.path} documents no JSON request body"
                continue
            }
            check(body.serializer.descriptor, schema, body.serializer.descriptor.serialName.substringAfterLast('.'), problems)
        }
        assertEquals("request fields the server's contract does not define:\n" + problems.joinToString("\n"), emptyList<String>(), problems)
    }

    /**
     * The table above names every class ServerApi and MusicApi encode as a body, so a
     * new request cannot skip this check by not being listed.
     */
    @Test
    fun everyEncodedBodyIsListed() {
        val sources = listOf("ServerApi.kt", "MusicApi.kt").map { File("src/main/kotlin/io/github/pete_builds/vcrx/tv/net/$it").readText() }
        val sent = sources.flatMap { source ->
            Regex("""post\([^\n]*?,\s*(\w+)\.serializer\(\)\)""").findAll(source).map { it.groupValues[1] } +
                Regex("""encodeToString\((\w+)\.serializer\(\)""").findAll(source).map { it.groupValues[1] }
        }.toSet()
        assertTrue("found no request bodies in the sources; the patterns are stale", sent.size >= 10)
        val listed = bodies.map { it.serializer.descriptor.serialName.substringAfterLast('.') }.toSet()
        assertEquals("request classes sent but not checked against openapi.yaml", emptySet<String>(), sent - listed)
    }

    /** Fails on the control: a class with a field its schema does not have. */
    @Test
    fun aFieldTheSchemaLacksIsCaught() {
        val schema = mapOf("type" to "object", "properties" to mapOf("played" to mapOf("type" to "boolean")))
        val problems = mutableListOf<String>()
        check(Extra.serializer().descriptor, schema, "Extra", problems)
        assertEquals(listOf("Extra.surprise"), problems)
    }

    @kotlinx.serialization.Serializable
    private data class Extra(val played: Boolean, val surprise: Int = 0)

    private fun check(descriptor: SerialDescriptor, rawSchema: Map<*, *>, at: String, problems: MutableList<String>) {
        val schema = resolve(rawSchema)
        when (descriptor.kind) {
            StructureKind.CLASS, StructureKind.OBJECT -> {
                val properties = properties(schema)
                if (properties == null) {
                    // A map-shaped object takes any key; an object with no properties takes none.
                    if (schema["additionalProperties"] == null) problems += "$at: the schema defines no properties"
                    return
                }
                for (i in 0 until descriptor.elementsCount) {
                    val name = descriptor.getElementName(i)
                    val property = properties[name] as Map<*, *>?
                    if (property == null) problems += "$at.$name" else check(descriptor.getElementDescriptor(i), property, "$at.$name", problems)
                }
            }
            StructureKind.LIST -> (schema["items"] as Map<*, *>?)?.let { check(descriptor.getElementDescriptor(0), it, "$at[]", problems) }
                ?: problems.add("$at: a list in the request, not an array in the schema")
            is PolymorphicKind, SerialKind.CONTEXTUAL -> problems += "$at: cannot check a ${descriptor.kind} field"
            else -> Unit // Primitives, enums and maps carry no field names of their own.
        }
    }

    /** Follows a \$ref; merges allOf; picks the one non-null choice of a oneOf or anyOf (or merges several). */
    private fun resolve(schema: Map<*, *>): Map<*, *> {
        (schema["\$ref"] as String?)?.let { ref ->
            val name = ref.removePrefix("#/components/schemas/")
            val target = spec.at("components", "schemas", name) as Map<*, *>? ?: error("unresolved \$ref $ref")
            return resolve(target)
        }
        val parts = (schema["allOf"] ?: schema["oneOf"] ?: schema["anyOf"]) as List<*>? ?: return schema
        val choices = parts.map { resolve(it as Map<*, *>) }.filter { it["type"] != "null" }
        val merged = LinkedHashMap<Any?, Any?>()
        val properties = LinkedHashMap<Any?, Any?>()
        for (choice in choices) {
            merged.putAll(choice)
            properties(choice)?.let { properties.putAll(it) }
        }
        properties(schema)?.let { properties.putAll(it) }
        if (properties.isNotEmpty()) merged["properties"] = properties
        return merged
    }

    private fun properties(schema: Map<*, *>): Map<*, *>? = schema["properties"] as Map<*, *>?

    private fun Map<*, *>.at(vararg keys: String): Any? = keys.fold<String, Any?>(this) { node, key -> (node as Map<*, *>?)?.get(key) }
}
